class AddZelloChannelPasswordToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :zello_channel_password, :string
  end
end
