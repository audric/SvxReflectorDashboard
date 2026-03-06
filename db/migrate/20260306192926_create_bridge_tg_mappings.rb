class CreateBridgeTgMappings < ActiveRecord::Migration[8.1]
  def change
    create_table :bridge_tg_mappings do |t|
      t.references :bridge, null: false, foreign_key: true
      t.integer :local_tg
      t.integer :remote_tg

      t.timestamps
    end
  end
end
