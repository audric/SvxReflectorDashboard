class CreateNodeEvents < ActiveRecord::Migration[7.1]
  def change
    create_table :node_events do |t|
      t.string  :callsign,   null: false
      t.string  :event_type, null: false  # talking_start | talking_stop | tg_join | tg_leave | connected | disconnected
      t.integer :tg
      t.string  :node_class
      t.string  :node_location
      t.timestamps
    end

    add_index :node_events, :callsign
    add_index :node_events, :event_type
    add_index :node_events, :created_at
    add_index :node_events, :tg
  end
end
