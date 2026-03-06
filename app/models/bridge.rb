class Bridge < ApplicationRecord
  validates :name, presence: true, uniqueness: true
  validates :remote_host, presence: true
  validates :remote_port, presence: true, numericality: { greater_than: 0 }
  validates :remote_callsign, presence: true
  validates :remote_auth_key, presence: true
  validates :remote_default_tg, presence: true, numericality: { greater_than: 0 }
  validates :local_host, presence: true
  validates :local_port, presence: true, numericality: { greater_than: 0 }
  validates :local_callsign, presence: true
  validates :local_auth_key, presence: true
  validates :local_default_tg, presence: true, numericality: { greater_than: 0 }
  validates :bridge_local_tg, presence: true, numericality: { greater_than: 0 }
  validates :bridge_remote_tg, presence: true, numericality: { greater_than: 0 }

  after_save :generate_config
  after_destroy :cleanup

  def config_dir
    Rails.root.join("bridge", id.to_s)
  end

  def config_path
    config_dir.join("svxlink.conf")
  end

  def container_name
    "svxlink-bridge-#{id}"
  end

  def generate_config
    FileUtils.mkdir_p(config_dir)
    lines = []

    lines << "[GLOBAL]"
    lines << "LOGICS=ReflectorLogicLocal,ReflectorLogicRemote"
    lines << "LINKS=BridgeLink"
    lines << "TIMESTAMP_FORMAT=\"%c\""
    lines << "CARD_SAMPLE_RATE=48000"
    lines << ""

    lines << "[ReflectorLogicLocal]"
    lines << "TYPE=Reflector"
    lines << "HOST=#{local_host}"
    lines << "PORT=#{local_port}"
    lines << "CALLSIGN=#{local_callsign}"
    lines << "AUTH_KEY=#{local_auth_key}"
    lines << "DEFAULT_TG=#{local_default_tg}"
    lines << ""

    lines << "[ReflectorLogicRemote]"
    lines << "TYPE=Reflector"
    lines << "HOST=#{remote_host}"
    lines << "PORT=#{remote_port}"
    lines << "CALLSIGN=#{remote_callsign}"
    lines << "AUTH_KEY=#{remote_auth_key}"
    lines << "DEFAULT_TG=#{remote_default_tg}"
    lines << ""

    lines << "[BridgeLink]"
    lines << "CONNECT_LOGICS=ReflectorLogicLocal:#{bridge_local_tg},ReflectorLogicRemote:#{bridge_remote_tg}"
    lines << "DEFAULT_CONNECT=1"
    lines << "TIMEOUT=#{timeout || 0}"
    lines << ""

    File.write(config_path, lines.join("\n"))
  end

  private

  def cleanup
    FileUtils.rm_rf(config_dir) if config_dir.exist?
  end
end
