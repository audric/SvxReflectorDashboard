class AddZelloFieldsToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :zello_username, :string
    add_column :bridges, :zello_password, :string
    add_column :bridges, :zello_channel, :string
    add_column :bridges, :zello_issuer_id, :string
    add_column :bridges, :zello_private_key, :text
  end
end
