class AddEchoLinkToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :bridge_type, :string, default: "reflector", null: false
    add_column :bridges, :echolink_callsign, :string
    add_column :bridges, :echolink_password, :string
    add_column :bridges, :echolink_sysopname, :string
    add_column :bridges, :echolink_location, :string
    add_column :bridges, :echolink_description, :text
    add_column :bridges, :echolink_max_qsos, :integer
    add_column :bridges, :echolink_max_connections, :integer
    add_column :bridges, :echolink_link_idle_timeout, :integer
    add_column :bridges, :echolink_proxy_server, :string
    add_column :bridges, :echolink_proxy_port, :integer
    add_column :bridges, :echolink_proxy_password, :string
    add_column :bridges, :echolink_autocon_echolink_id, :string
    add_column :bridges, :echolink_autocon_time, :integer
    add_column :bridges, :echolink_accept_incoming, :string
    add_column :bridges, :echolink_reject_incoming, :string
    add_column :bridges, :echolink_drop_incoming, :string
    add_column :bridges, :echolink_accept_outgoing, :string
    add_column :bridges, :echolink_reject_outgoing, :string
    add_column :bridges, :echolink_reject_conf, :boolean
    add_column :bridges, :echolink_use_gsm_only, :boolean
    add_column :bridges, :echolink_bind_addr, :string
    add_column :bridges, :echolink_servers, :string
  end
end
