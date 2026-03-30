# This file is auto-generated from the current state of the database. Instead
# of editing this file, please use the migrations feature of Active Record to
# incrementally modify your database, and then regenerate this schema definition.
#
# This file is the source Rails uses to define your schema when running `bin/rails
# db:schema:load`. When creating a new database, `bin/rails db:schema:load` tends to
# be faster and is potentially less error prone than running all of your
# migrations from scratch. Old migrations may fail to apply correctly if those
# migrations use external dependencies or application code.
#
# It's strongly recommended that you check this file into your version control system.

ActiveRecord::Schema[8.1].define(version: 2026_03_30_145538) do
  create_table "bridge_tg_mappings", force: :cascade do |t|
    t.string "activate_on_activity"
    t.integer "bridge_id", null: false
    t.datetime "created_at", null: false
    t.boolean "default_active", default: true
    t.integer "local_tg"
    t.integer "remote_tg"
    t.integer "timeout"
    t.datetime "updated_at", null: false
    t.index ["bridge_id"], name: "index_bridge_tg_mappings_on_bridge_id"
  end

  create_table "bridges", force: :cascade do |t|
    t.float "agc_attack_rate"
    t.float "agc_decay_rate"
    t.float "agc_limit_level"
    t.float "agc_max_gain"
    t.float "agc_min_gain"
    t.float "agc_target_level"
    t.string "allstar_node"
    t.string "allstar_password"
    t.integer "allstar_port"
    t.string "allstar_server"
    t.integer "bridge_local_tg"
    t.integer "bridge_remote_tg"
    t.string "bridge_type", default: "reflector", null: false
    t.string "cert_email"
    t.string "cert_subj_c"
    t.string "cert_subj_gn"
    t.string "cert_subj_l"
    t.string "cert_subj_o"
    t.string "cert_subj_ou"
    t.string "cert_subj_sn"
    t.string "cert_subj_st"
    t.datetime "created_at", null: false
    t.boolean "default_active", default: true
    t.string "dmr_callsign"
    t.integer "dmr_color_code"
    t.string "dmr_host"
    t.integer "dmr_id"
    t.string "dmr_password"
    t.integer "dmr_port"
    t.integer "dmr_talkgroup"
    t.integer "dmr_timeslot"
    t.string "echolink_accept_incoming"
    t.string "echolink_accept_outgoing"
    t.string "echolink_autocon_echolink_id"
    t.integer "echolink_autocon_time"
    t.string "echolink_bind_addr"
    t.string "echolink_callsign"
    t.text "echolink_description"
    t.string "echolink_drop_incoming"
    t.integer "echolink_link_idle_timeout"
    t.string "echolink_location"
    t.integer "echolink_max_connections"
    t.integer "echolink_max_qsos"
    t.string "echolink_password"
    t.string "echolink_proxy_password"
    t.integer "echolink_proxy_port"
    t.string "echolink_proxy_server"
    t.boolean "echolink_reject_conf"
    t.string "echolink_reject_incoming"
    t.string "echolink_reject_outgoing"
    t.string "echolink_servers"
    t.string "echolink_sysopname"
    t.boolean "echolink_use_gsm_only"
    t.boolean "enabled", default: false
    t.float "filter_hpf_cutoff"
    t.float "filter_lpf_cutoff"
    t.string "iax_codecs", default: "gsm,ulaw,alaw,g726"
    t.string "iax_context", default: "friend"
    t.string "iax_extension"
    t.integer "iax_idle_timeout", default: 30
    t.string "iax_mode", default: "persistent"
    t.string "iax_password"
    t.integer "iax_port", default: 4569
    t.string "iax_server"
    t.string "iax_username"
    t.integer "jitter_buffer_delay"
    t.string "local_auth_key"
    t.string "local_callsign"
    t.integer "local_default_tg"
    t.string "local_host"
    t.integer "local_port"
    t.string "m17_callsign"
    t.string "m17_host"
    t.string "m17_module"
    t.integer "m17_port"
    t.string "monitor_tgs"
    t.boolean "mute_first_tx_loc"
    t.boolean "mute_first_tx_rem"
    t.string "name"
    t.string "node_location"
    t.string "nxdn_host"
    t.integer "nxdn_id"
    t.integer "nxdn_port"
    t.integer "nxdn_talkgroup"
    t.string "p25_host"
    t.integer "p25_id"
    t.integer "p25_port"
    t.integer "p25_talkgroup"
    t.string "remote_auth_key"
    t.text "remote_ca_bundle"
    t.string "remote_callsign"
    t.integer "remote_default_tg"
    t.string "remote_host"
    t.integer "remote_port"
    t.string "sip_caller_id"
    t.string "sip_codecs", default: "opus,g722,gsm,ulaw,alaw"
    t.string "sip_dtmf"
    t.integer "sip_dtmf_delay", default: 2000
    t.string "sip_extension"
    t.integer "sip_idle_timeout", default: 30
    t.integer "sip_log_level", default: 1
    t.string "sip_mode", default: "persistent"
    t.string "sip_password"
    t.string "sip_pin"
    t.integer "sip_pin_timeout", default: 10
    t.integer "sip_port", default: 5060
    t.string "sip_server"
    t.string "sip_transport", default: "udp"
    t.string "sip_username"
    t.string "sysop"
    t.integer "tg_select_timeout"
    t.integer "timeout"
    t.integer "udp_heartbeat_interval"
    t.datetime "updated_at", null: false
    t.boolean "verbose"
    t.string "xlx_callsign"
    t.string "xlx_callsign_suffix"
    t.integer "xlx_dmr_id"
    t.string "xlx_host"
    t.string "xlx_module"
    t.string "xlx_mycall"
    t.string "xlx_mycall_suffix"
    t.integer "xlx_port"
    t.string "xlx_protocol", default: "DCS"
    t.string "xlx_reflector_name"
    t.string "ysf_callsign"
    t.string "ysf_description"
    t.string "ysf_host"
    t.integer "ysf_port"
    t.string "zello_channel"
    t.string "zello_channel_password"
    t.string "zello_issuer_id"
    t.string "zello_password"
    t.text "zello_private_key"
    t.string "zello_username"
  end

  create_table "ctcss_tones", force: :cascade do |t|
    t.string "code", null: false
    t.datetime "created_at", null: false
    t.decimal "frequency", precision: 5, scale: 1, null: false
    t.datetime "updated_at", null: false
    t.index ["code"], name: "index_ctcss_tones_on_code", unique: true
    t.index ["frequency"], name: "index_ctcss_tones_on_frequency", unique: true
  end

  create_table "external_reflectors", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.text "description"
    t.boolean "enabled", default: true
    t.string "name", null: false
    t.string "portal_url"
    t.string "status_url", null: false
    t.datetime "updated_at", null: false
  end

  create_table "node_classes", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "name", null: false
    t.datetime "updated_at", null: false
    t.index ["name"], name: "index_node_classes_on_name", unique: true
  end

  create_table "node_events", force: :cascade do |t|
    t.string "callsign", null: false
    t.datetime "created_at", null: false
    t.string "event_type", null: false
    t.text "metadata"
    t.string "node_class"
    t.string "node_location"
    t.string "source"
    t.integer "tg"
    t.datetime "updated_at", null: false
    t.index ["callsign"], name: "index_node_events_on_callsign"
    t.index ["created_at"], name: "index_node_events_on_created_at"
    t.index ["event_type"], name: "index_node_events_on_event_type"
    t.index ["source"], name: "index_node_events_on_source"
    t.index ["tg"], name: "index_node_events_on_tg"
  end

  create_table "node_infos", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "grid_locator"
    t.boolean "hidden", default: false
    t.float "latitude"
    t.float "longitude"
    t.string "node_class"
    t.string "node_location"
    t.string "qth_name"
    t.string "rx_ant_comment"
    t.integer "rx_ant_dir"
    t.integer "rx_ant_height"
    t.string "rx_ctcss_freqs"
    t.float "rx_freq"
    t.string "rx_name"
    t.string "rx_sql_type"
    t.string "sysop"
    t.text "tone_to_talkgroup"
    t.string "tx_ant_comment"
    t.integer "tx_ant_dir"
    t.integer "tx_ant_height"
    t.float "tx_ctcss_freq"
    t.float "tx_freq"
    t.string "tx_name"
    t.float "tx_pwr"
    t.datetime "updated_at", null: false
    t.integer "user_id", null: false
    t.index ["user_id"], name: "index_node_infos_on_user_id", unique: true
  end

  create_table "nodes", force: :cascade do |t|
    t.string "callsign", null: false
    t.datetime "created_at", null: false
    t.string "locator"
    t.text "monitored_tgs"
    t.integer "node_class_id"
    t.string "node_location"
    t.string "rx_freq"
    t.string "sysop"
    t.integer "talkgroup_id"
    t.text "tone_to_talkgroup"
    t.string "tx_freq"
    t.datetime "updated_at", null: false
    t.index ["callsign"], name: "index_nodes_on_callsign", unique: true
    t.index ["node_class_id"], name: "index_nodes_on_node_class_id"
    t.index ["talkgroup_id"], name: "index_nodes_on_talkgroup_id"
  end

  create_table "settings", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "key", null: false
    t.datetime "updated_at", null: false
    t.string "value"
    t.index ["key"], name: "index_settings_on_key", unique: true
  end

  create_table "talkgroups", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "name"
    t.integer "number", null: false
    t.datetime "updated_at", null: false
    t.index ["number"], name: "index_talkgroups_on_number", unique: true
  end

  create_table "tgs", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.text "description"
    t.string "kind", default: "local"
    t.string "name"
    t.string "tg"
    t.datetime "updated_at", null: false
  end

  create_table "users", force: :cascade do |t|
    t.boolean "approved", default: false, null: false
    t.string "callsign", null: false
    t.boolean "can_monitor", default: false, null: false
    t.boolean "can_transmit", default: false, null: false
    t.datetime "created_at", null: false
    t.boolean "cw_roger_beep", default: false, null: false
    t.string "email"
    t.datetime "last_sign_in_at"
    t.string "mobile"
    t.string "name"
    t.string "password_digest", null: false
    t.boolean "reflector_admin", default: false, null: false
    t.string "reflector_auth_key"
    t.string "role", default: "user", null: false
    t.string "telegram"
    t.datetime "updated_at", null: false
    t.index ["callsign"], name: "index_users_on_callsign", unique: true
  end

  add_foreign_key "bridge_tg_mappings", "bridges"
  add_foreign_key "node_infos", "users"
  add_foreign_key "nodes", "node_classes"
  add_foreign_key "nodes", "talkgroups"
end
