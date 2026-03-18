class AddM17NxdnP25AllstarFieldsToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :m17_host, :string
    add_column :bridges, :m17_port, :integer
    add_column :bridges, :m17_callsign, :string
    add_column :bridges, :m17_module, :string
    add_column :bridges, :nxdn_host, :string
    add_column :bridges, :nxdn_port, :integer
    add_column :bridges, :nxdn_id, :integer
    add_column :bridges, :nxdn_talkgroup, :integer
    add_column :bridges, :p25_host, :string
    add_column :bridges, :p25_port, :integer
    add_column :bridges, :p25_id, :integer
    add_column :bridges, :p25_talkgroup, :integer
    add_column :bridges, :allstar_node, :string
    add_column :bridges, :allstar_password, :string
    add_column :bridges, :allstar_server, :string
    add_column :bridges, :allstar_port, :integer
  end
end
