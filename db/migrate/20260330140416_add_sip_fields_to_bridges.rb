class AddSipFieldsToBridges < ActiveRecord::Migration[8.0]
  def change
    add_column :bridges, :sip_username, :string
    add_column :bridges, :sip_password, :string
    add_column :bridges, :sip_server, :string
    add_column :bridges, :sip_port, :integer, default: 5060
    add_column :bridges, :sip_extension, :string
    add_column :bridges, :sip_transport, :string, default: "udp"
    add_column :bridges, :sip_mode, :string, default: "persistent"
    add_column :bridges, :sip_idle_timeout, :integer, default: 30
    add_column :bridges, :sip_codecs, :string, default: "opus,g722,gsm,ulaw,alaw"
    add_column :bridges, :sip_dtmf, :string
    add_column :bridges, :sip_dtmf_delay, :integer, default: 2000
    add_column :bridges, :sip_caller_id, :string
    add_column :bridges, :sip_log_level, :integer, default: 1
  end
end
