class CreateCtcssTones < ActiveRecord::Migration[7.1]
  def change
    create_table :ctcss_tones do |t|
      t.decimal :frequency, precision: 5, scale: 1, null: false
      t.string :code, null: false
      t.timestamps
    end

    add_index :ctcss_tones, :frequency, unique: true
    add_index :ctcss_tones, :code, unique: true
  end
end
