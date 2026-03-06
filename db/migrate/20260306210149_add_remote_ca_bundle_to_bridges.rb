class AddRemoteCaBundleToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :remote_ca_bundle, :text
  end
end
