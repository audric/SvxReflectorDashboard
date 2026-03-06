class CreateBridges < ActiveRecord::Migration[8.1]
  def change
    create_table :bridges do |t|
      t.string :name
      t.string :local_host
      t.integer :local_port
      t.string :local_callsign
      t.string :local_auth_key
      t.integer :local_default_tg
      t.string :remote_host
      t.integer :remote_port
      t.string :remote_callsign
      t.string :remote_auth_key
      t.integer :remote_default_tg
      t.integer :bridge_local_tg
      t.integer :bridge_remote_tg
      t.integer :timeout
      t.boolean :enabled, default: false

      t.timestamps
    end
  end
end
