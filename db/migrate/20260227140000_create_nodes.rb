class CreateNodes < ActiveRecord::Migration[7.1]
  def change
    create_table :nodes do |t|
      t.string :callsign, null: false
      t.integer :tg
      t.boolean :is_talker, default: false
      t.string :node_class
      t.string :node_location
      t.boolean :hidden, default: false
      t.string :sysop
      t.string :sw
      t.string :sw_ver
      t.string :rx_freq
      t.string :tx_freq
      t.string :locator
      t.text :monitored_tgs
      t.text :tone_to_talkgroup

      t.timestamps
    end

    add_index :nodes, :callsign, unique: true
  end
end
