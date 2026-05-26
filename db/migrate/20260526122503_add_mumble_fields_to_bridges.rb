class AddMumbleFieldsToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :mumble_host, :string
    add_column :bridges, :mumble_port, :integer, default: 64738
    add_column :bridges, :mumble_channel, :string
    add_column :bridges, :mumble_bot_password, :string
  end
end
