/*
 * sip_helper — PJSUA wrapper for the SIP bridge.
 *
 * Commands (stdin):  CALL sip:ext@server | ANSWER | HANGUP | DTMF digits | QUIT
 * Events (stdout):   REGISTERED | REG_FAILED reason | INCOMING uri | CONNECTED | DISCONNECTED | DTMF_RECEIVED digit
 * Audio in  (fd 3):  PCM 16-bit LE, 8kHz mono, 320 bytes/frame (20ms)
 * Audio out (fd 4):  same format
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <time.h>
#include <sys/select.h>
#include <pthread.h>
#include <pjsua-lib/pjsua.h>

#define CLOCK_RATE   8000
#define SAMPLES      160   /* 20ms */
#define FRAME_BYTES  320   /* 160 samples * 2 bytes */
#define THIS_FILE    "sip_helper"

/* ── Globals ── */
static pjsua_acc_id   g_acc_id = PJSUA_INVALID_ID;
static pjsua_call_id  g_call_id = PJSUA_INVALID_ID;
static int             g_audio_in_fd  = -1;  /* fd 3: Go → PJSIP */
static int             g_audio_out_fd = -1;  /* fd 4: PJSIP → Go */
static pj_pool_t      *g_pool = NULL;
static pjmedia_port   *g_bridge_port = NULL;
static pj_bool_t       g_quit = PJ_FALSE;
static pjsua_conf_port_id g_bridge_slot = PJSUA_INVALID_ID;

/* ── PIN gate ── */
static char            g_pin[32] = "";          /* empty = no PIN required */
static int             g_pin_timeout = 10;      /* seconds */
static char            g_pin_buf[32];           /* collected DTMF digits */
static int             g_pin_pos = 0;
static pj_bool_t       g_pin_pending = PJ_FALSE; /* waiting for PIN entry */
static time_t          g_pin_start = 0;         /* when PIN collection started */
static time_t          g_hangup_at = 0;         /* deferred hangup after fail tone */

/* ── Tone generator for PIN feedback ── */
static pjmedia_port           *g_tonegen = NULL;
static pjsua_conf_port_id      g_tone_slot = PJSUA_INVALID_ID;

static void play_tone(unsigned freq1, unsigned freq2, unsigned on_ms, unsigned off_ms, unsigned count) {
    if (!g_tonegen) { PJ_LOG(2,(THIS_FILE, "play_tone: no tonegen")); return; }
    if (g_call_id == PJSUA_INVALID_ID) { PJ_LOG(2,(THIS_FILE, "play_tone: no call")); return; }

    pjsua_call_info ci;
    pjsua_call_get_info(g_call_id, &ci);
    if (ci.media_status != PJSUA_CALL_MEDIA_ACTIVE) { PJ_LOG(2,(THIS_FILE, "play_tone: media not active")); return; }

    /* Connect tone generator to the call */
    pj_status_t st = pjsua_conf_connect(g_tone_slot, ci.conf_slot);
    PJ_LOG(3,(THIS_FILE, "play_tone: %dHz %dms x%d, connect=%d, call_slot=%d", freq1, on_ms, count, st, ci.conf_slot));

    pjmedia_tone_desc tones[1];
    pj_bzero(tones, sizeof(tones));
    tones[0].freq1 = (short)freq1;
    tones[0].freq2 = (short)freq2;
    tones[0].on_msec = (short)on_ms;
    tones[0].off_msec = (short)off_ms;
    tones[0].volume = 0; /* 0 = default */
    pjmedia_tonegen_play(g_tonegen, count, tones, 0);
}

/* Short beep: "ready for PIN" */
static void play_prompt_tone(void) {
    play_tone(800, 0, 200, 0, 1);
}

/* Rising two-tone: "PIN accepted" */
static void play_ok_tone(void) {
    if (!g_tonegen || g_call_id == PJSUA_INVALID_ID) return;
    pjsua_call_info ci;
    pjsua_call_get_info(g_call_id, &ci);
    if (ci.media_status != PJSUA_CALL_MEDIA_ACTIVE) return;
    pjsua_conf_connect(g_tone_slot, ci.conf_slot);

    pjmedia_tone_desc tones[2];
    pj_bzero(tones, sizeof(tones));
    tones[0].freq1 = 800;  tones[0].on_msec = 100; tones[0].off_msec = 50;
    tones[1].freq1 = 1200; tones[1].on_msec = 200; tones[1].off_msec = 0;
    pjmedia_tonegen_play(g_tonegen, 2, tones, 0);
}

/* Low buzz: "PIN rejected" */
static void play_fail_tone(void) {
    play_tone(400, 0, 300, 100, 2);
}

/* Thread-safe event output */
static pthread_mutex_t g_stdout_mu = PTHREAD_MUTEX_INITIALIZER;

static void emit(const char *fmt, ...) {
    va_list ap;
    pthread_mutex_lock(&g_stdout_mu);
    va_start(ap, fmt);
    vfprintf(stdout, fmt, ap);
    va_end(ap);
    fprintf(stdout, "\n");
    fflush(stdout);
    pthread_mutex_unlock(&g_stdout_mu);
}

/* ── Custom media port ── */

static pj_status_t bridge_get_frame(pjmedia_port *port, pjmedia_frame *frame) {
    /* PJSIP wants audio to send to remote — read from fd 3 (Go writes here) */
    (void)port;
    frame->type = PJMEDIA_FRAME_TYPE_AUDIO;
    frame->size = FRAME_BYTES;

    ssize_t total = 0;
    while (total < FRAME_BYTES) {
        ssize_t n = read(g_audio_in_fd, (char*)frame->buf + total, FRAME_BYTES - total);
        if (n <= 0) {
            /* No data — send silence */
            memset((char*)frame->buf + total, 0, FRAME_BYTES - total);
            break;
        }
        total += n;
    }
    return PJ_SUCCESS;
}

static pj_status_t bridge_put_frame(pjmedia_port *port, pjmedia_frame *frame) {
    /* PJSIP received audio from remote — write to fd 4 (Go reads here) */
    (void)port;
    if (frame->type != PJMEDIA_FRAME_TYPE_AUDIO || frame->size == 0)
        return PJ_SUCCESS;

    ssize_t total = 0;
    ssize_t to_write = (ssize_t)frame->size;
    while (total < to_write) {
        ssize_t n = write(g_audio_out_fd, (char*)frame->buf + total, to_write - total);
        if (n <= 0) break;
        total += n;
    }
    return PJ_SUCCESS;
}

static pjmedia_port* create_bridge_port(pj_pool_t *pool) {
    pjmedia_port *port = pj_pool_zalloc(pool, sizeof(pjmedia_port));
    pjmedia_port_info_init(&port->info, &(pj_str_t){.ptr="bridge", .slen=6},
                           0x12345678, CLOCK_RATE, 1, 16, SAMPLES);
    port->get_frame = &bridge_get_frame;
    port->put_frame = &bridge_put_frame;
    return port;
}

/* ── PJSUA callbacks ── */

static void on_reg_state(pjsua_acc_id acc_id) {
    pjsua_acc_info info;
    pjsua_acc_get_info(acc_id, &info);
    if (info.status == 200) {
        emit("REGISTERED");
    } else {
        emit("REG_FAILED %d %.*s", info.status,
             (int)info.status_text.slen, info.status_text.ptr);
    }
}

static void on_incoming_call(pjsua_acc_id acc_id, pjsua_call_id call_id,
                             pjsip_rx_data *rdata) {
    (void)acc_id;
    (void)rdata;
    pjsua_call_info ci;
    pjsua_call_get_info(call_id, &ci);
    g_call_id = call_id;

    /* Start PIN gate if configured */
    if (g_pin[0] != '\0') {
        g_pin_pending = PJ_TRUE;
        g_pin_pos = 0;
        g_pin_buf[0] = '\0';
        g_pin_start = time(NULL);
    }

    emit("INCOMING %.*s", (int)ci.remote_info.slen, ci.remote_info.ptr);
}

static void on_call_state(pjsua_call_id call_id, pjsip_event *e) {
    (void)e;
    pjsua_call_info ci;
    pjsua_call_get_info(call_id, &ci);

    if (ci.state == PJSIP_INV_STATE_DISCONNECTED) {
        g_call_id = PJSUA_INVALID_ID;
        g_pin_pending = PJ_FALSE;
        g_pin_pos = 0;
        g_hangup_at = 0;
        g_bridge_slot = PJSUA_INVALID_ID;
        emit("DISCONNECTED");
    }
}

/* Connect audio bridge port to the active call */
static void connect_audio_bridge(void) {
    if (g_call_id == PJSUA_INVALID_ID) return;
    pjsua_call_info ci;
    pjsua_call_get_info(g_call_id, &ci);
    if (ci.media_status != PJSUA_CALL_MEDIA_ACTIVE) return;

    if (g_bridge_slot == PJSUA_INVALID_ID) {
        pjsua_conf_add_port(g_pool, g_bridge_port, &g_bridge_slot);
    }
    pjsua_conf_connect(ci.conf_slot, g_bridge_slot);  /* remote → bridge → Go */
    pjsua_conf_connect(g_bridge_slot, ci.conf_slot);  /* Go → bridge → remote */
    emit("CONNECTED");
}

static void on_call_media_state(pjsua_call_id call_id) {
    pjsua_call_info ci;
    pjsua_call_get_info(call_id, &ci);

    if (ci.media_status == PJSUA_CALL_MEDIA_ACTIVE) {
        if (g_pin_pending) {
            /* PIN gate active — don't connect audio yet, play prompt tone */
            PJ_LOG(3,(THIS_FILE, "Media active, waiting for PIN..."));
            play_prompt_tone();
        } else {
            connect_audio_bridge();
        }
    }
}

static void handle_dtmf(int digit) {
    emit("DTMF_RECEIVED %c", (char)digit);

    if (g_pin_pending && g_pin_pos < (int)sizeof(g_pin_buf) - 1) {
        g_pin_buf[g_pin_pos++] = (char)digit;
        g_pin_buf[g_pin_pos] = '\0';

        /* Check if collected digits match the PIN */
        if (strcmp(g_pin_buf, g_pin) == 0) {
            g_pin_pending = PJ_FALSE;
            PJ_LOG(3,(THIS_FILE, "PIN accepted"));
            emit("PIN_OK");
            play_ok_tone();
            connect_audio_bridge();
        }
        /* If they've entered more digits than the PIN length, wrong PIN */
        else if (g_pin_pos >= (int)strlen(g_pin)) {
            g_pin_pending = PJ_FALSE;
            PJ_LOG(3,(THIS_FILE, "Wrong PIN"));
            play_fail_tone();
            emit("PIN_FAILED");
            g_hangup_at = time(NULL) + 1; /* hangup after 1s so caller hears tone */
        }
    }
}

/* Handles both RFC 2833 and SIP INFO DTMF */
static void on_dtmf_digit2(pjsua_call_id call_id,
                            const pjsua_dtmf_info *info) {
    (void)call_id;
    handle_dtmf(info->digit);
}

/* ── Codec configuration ── */

static void configure_codecs(const char *codec_list) {
    /* Disable all codecs first */
    pjsua_codec_info codecs[64];
    unsigned count = 64;
    pjsua_enum_codecs(codecs, &count);
    for (unsigned i = 0; i < count; i++) {
        pjsua_codec_set_priority(&codecs[i].codec_id, 0);
    }

    /* Enable requested codecs in priority order */
    struct { const char *name; const char *pjid; } map[] = {
        {"opus",  "opus/48000"},
        {"g722",  "G722/16000"},
        {"gsm",   "GSM/8000"},
        {"ulaw",  "PCMU/8000"},
        {"alaw",  "PCMA/8000"},
        {NULL, NULL}
    };

    char buf[256];
    strncpy(buf, codec_list, sizeof(buf)-1);
    buf[sizeof(buf)-1] = 0;

    int prio = 255;
    char *saveptr = NULL;
    char *tok = strtok_r(buf, ",", &saveptr);
    while (tok && prio > 0) {
        while (*tok == ' ') tok++;
        char *end = tok + strlen(tok) - 1;
        while (end > tok && *end == ' ') *end-- = 0;

        for (int i = 0; map[i].name; i++) {
            if (strcasecmp(tok, map[i].name) == 0) {
                pj_str_t cid = pj_str((char*)map[i].pjid);
                pjsua_codec_set_priority(&cid, (pj_uint8_t)prio);
                prio--;
                break;
            }
        }
        tok = strtok_r(NULL, ",", &saveptr);
    }
}

/* ── Command processing (stdin) ── */

static void process_command(char *line) {
    char *nl = strchr(line, '\n');
    if (nl) *nl = 0;
    nl = strchr(line, '\r');
    if (nl) *nl = 0;

    if (strncmp(line, "CALL ", 5) == 0) {
        if (g_call_id != PJSUA_INVALID_ID) {
            PJ_LOG(3,(THIS_FILE, "Already in a call, ignoring CALL"));
            return;
        }
        pj_str_t uri = pj_str(line + 5);
        pj_status_t status = pjsua_call_make_call(g_acc_id, &uri, 0, NULL, NULL, &g_call_id);
        if (status != PJ_SUCCESS) {
            PJ_LOG(2,(THIS_FILE, "Call failed: %d", status));
            g_call_id = PJSUA_INVALID_ID;
            emit("DISCONNECTED");
        }
    }
    else if (strcmp(line, "ANSWER") == 0) {
        if (g_call_id != PJSUA_INVALID_ID) {
            pjsua_call_answer(g_call_id, 200, NULL, NULL);
        }
    }
    else if (strcmp(line, "HANGUP") == 0) {
        if (g_call_id != PJSUA_INVALID_ID) {
            pjsua_call_hangup(g_call_id, 0, NULL, NULL);
            g_call_id = PJSUA_INVALID_ID;
        }
    }
    else if (strncmp(line, "DTMF ", 5) == 0) {
        if (g_call_id != PJSUA_INVALID_ID) {
            pj_str_t digits = pj_str(line + 5);
            pjsua_call_dial_dtmf(g_call_id, &digits);
        }
    }
    else if (strcmp(line, "QUIT") == 0) {
        g_quit = PJ_TRUE;
    }
}

/* ── Main ── */

int main(void) {
    pj_status_t status;

    g_audio_in_fd = 3;
    g_audio_out_fd = 4;

    const char *username   = getenv("SIP_USERNAME");
    const char *password   = getenv("SIP_PASSWORD");
    const char *server     = getenv("SIP_SERVER");
    const char *port_str   = getenv("SIP_PORT");
    const char *transport  = getenv("SIP_TRANSPORT");
    const char *codecs     = getenv("SIP_CODECS");
    const char *caller_id  = getenv("SIP_CALLER_ID");
    const char *log_level  = getenv("SIP_LOG_LEVEL");
    const char *pin_env    = getenv("SIP_PIN");
    const char *pin_to_env = getenv("SIP_PIN_TIMEOUT");

    /* Configure PIN gate */
    if (pin_env && pin_env[0]) {
        strncpy(g_pin, pin_env, sizeof(g_pin) - 1);
        g_pin[sizeof(g_pin) - 1] = '\0';
    }
    if (pin_to_env) g_pin_timeout = atoi(pin_to_env);
    if (g_pin_timeout < 3) g_pin_timeout = 10;

    if (!username || !password || !server) {
        fprintf(stderr, "SIP_USERNAME, SIP_PASSWORD, SIP_SERVER required\n");
        return 1;
    }

    int port = port_str ? atoi(port_str) : 5060;
    int loglevel = log_level ? atoi(log_level) : 1;

    status = pjsua_create();
    if (status != PJ_SUCCESS) {
        fprintf(stderr, "pjsua_create failed: %d\n", status);
        return 1;
    }

    pjsua_config cfg;
    pjsua_config_default(&cfg);
    cfg.cb.on_reg_state = &on_reg_state;
    cfg.cb.on_incoming_call = &on_incoming_call;
    cfg.cb.on_call_state = &on_call_state;
    cfg.cb.on_call_media_state = &on_call_media_state;
    cfg.cb.on_dtmf_digit2 = &on_dtmf_digit2;
    cfg.max_calls = 1;

    pjsua_logging_config log_cfg;
    pjsua_logging_config_default(&log_cfg);
    log_cfg.console_level = loglevel;
    log_cfg.level = loglevel;

    pjsua_media_config media_cfg;
    pjsua_media_config_default(&media_cfg);
    media_cfg.clock_rate = CLOCK_RATE;
    media_cfg.snd_clock_rate = CLOCK_RATE;
    media_cfg.no_vad = PJ_TRUE;
    media_cfg.ec_tail_len = 0;

    status = pjsua_init(&cfg, &log_cfg, &media_cfg);
    if (status != PJ_SUCCESS) {
        fprintf(stderr, "pjsua_init failed: %d\n", status);
        pjsua_destroy();
        return 1;
    }

    g_pool = pjsua_pool_create("sip_helper", 4096, 4096);
    g_bridge_port = create_bridge_port(g_pool);

    /* Create tone generator for PIN audio feedback */
    status = pjmedia_tonegen_create(g_pool, CLOCK_RATE, 1, SAMPLES, 16, 0, &g_tonegen);
    if (status == PJ_SUCCESS) {
        status = pjsua_conf_add_port(g_pool, g_tonegen, &g_tone_slot);
        PJ_LOG(3,(THIS_FILE, "Tone generator created, slot=%d", g_tone_slot));
    } else {
        PJ_LOG(2,(THIS_FILE, "Tone generator FAILED: %d", status));
    }

    pjsua_set_null_snd_dev();

    pjsua_transport_config tp_cfg;
    pjsua_transport_config_default(&tp_cfg);

    pjsip_transport_type_e tp_type = PJSIP_TRANSPORT_UDP;
    if (transport) {
        if (strcasecmp(transport, "tcp") == 0) tp_type = PJSIP_TRANSPORT_TCP;
        else if (strcasecmp(transport, "tls") == 0) tp_type = PJSIP_TRANSPORT_TLS;
    }

    status = pjsua_transport_create(tp_type, &tp_cfg, NULL);
    if (status != PJ_SUCCESS) {
        fprintf(stderr, "Transport create failed: %d\n", status);
        pjsua_destroy();
        return 1;
    }

    status = pjsua_start();
    if (status != PJ_SUCCESS) {
        fprintf(stderr, "pjsua_start failed: %d\n", status);
        pjsua_destroy();
        return 1;
    }

    if (codecs) configure_codecs(codecs);

    char id_buf[256], reg_buf[256];
    if (caller_id && caller_id[0]) {
        snprintf(id_buf, sizeof(id_buf), "\"%s\" <sip:%s@%s>", caller_id, username, server);
    } else {
        snprintf(id_buf, sizeof(id_buf), "sip:%s@%s", username, server);
    }
    snprintf(reg_buf, sizeof(reg_buf), "sip:%s:%d", server, port);

    pjsua_acc_config acc_cfg;
    pjsua_acc_config_default(&acc_cfg);
    acc_cfg.id = pj_str(id_buf);
    acc_cfg.reg_uri = pj_str(reg_buf);
    acc_cfg.cred_count = 1;
    acc_cfg.cred_info[0].realm = pj_str("*");
    acc_cfg.cred_info[0].scheme = pj_str("digest");
    acc_cfg.cred_info[0].username = pj_str((char*)username);
    acc_cfg.cred_info[0].data_type = PJSIP_CRED_DATA_PLAIN_PASSWD;
    acc_cfg.cred_info[0].data = pj_str((char*)password);

    if (tp_type == PJSIP_TRANSPORT_TLS) {
        char proxy_buf[256];
        snprintf(proxy_buf, sizeof(proxy_buf), "sip:%s:%d;transport=tls", server, port);
        acc_cfg.proxy[acc_cfg.proxy_cnt++] = pj_str(proxy_buf);
    } else if (tp_type == PJSIP_TRANSPORT_TCP) {
        char proxy_buf[256];
        snprintf(proxy_buf, sizeof(proxy_buf), "sip:%s:%d;transport=tcp", server, port);
        acc_cfg.proxy[acc_cfg.proxy_cnt++] = pj_str(proxy_buf);
    }

    status = pjsua_acc_add(&acc_cfg, PJ_TRUE, &g_acc_id);
    if (status != PJ_SUCCESS) {
        fprintf(stderr, "Account add failed: %d\n", status);
        pjsua_destroy();
        return 1;
    }

    /* Command loop with PIN timeout checking */
    char line[512];
    while (!g_quit) {
        fd_set rfds;
        struct timeval tv;
        FD_ZERO(&rfds);
        FD_SET(STDIN_FILENO, &rfds);
        tv.tv_sec = 1;
        tv.tv_usec = 0;

        int ret = select(STDIN_FILENO + 1, &rfds, NULL, NULL, &tv);
        if (ret > 0 && FD_ISSET(STDIN_FILENO, &rfds)) {
            if (fgets(line, sizeof(line), stdin) == NULL) break;
            process_command(line);
        }

        /* Check PIN timeout */
        if (g_pin_pending && g_pin_start > 0) {
            if (time(NULL) - g_pin_start >= g_pin_timeout) {
                g_pin_pending = PJ_FALSE;
                PJ_LOG(3,(THIS_FILE, "PIN timeout"));
                play_fail_tone();
                emit("PIN_FAILED");
                g_hangup_at = time(NULL) + 1;
            }
        }

        /* Deferred hangup after fail/timeout tone */
        if (g_hangup_at > 0 && time(NULL) >= g_hangup_at) {
            g_hangup_at = 0;
            if (g_call_id != PJSUA_INVALID_ID) {
                pjsua_call_hangup(g_call_id, 0, NULL, NULL);
                g_call_id = PJSUA_INVALID_ID;
            }
        }
    }

    if (g_call_id != PJSUA_INVALID_ID) {
        pjsua_call_hangup(g_call_id, 0, NULL, NULL);
    }
    pjsua_destroy();
    return 0;
}
