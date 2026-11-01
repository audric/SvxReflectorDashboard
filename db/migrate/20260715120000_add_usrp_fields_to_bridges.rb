class AddUsrpFieldsToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :usrp_host, :string
    add_column :bridges, :usrp_tx_port, :integer, default: 41234
    add_column :bridges, :usrp_rx_port, :integer, default: 41233
  end
end
