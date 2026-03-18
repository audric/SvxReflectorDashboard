class AddYsfFieldsToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :ysf_host, :string
    add_column :bridges, :ysf_port, :integer
    add_column :bridges, :ysf_callsign, :string
    add_column :bridges, :ysf_description, :string
  end
end
