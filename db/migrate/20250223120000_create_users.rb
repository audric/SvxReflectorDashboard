class CreateUsers < ActiveRecord::Migration[7.1]
  def change
    create_table :users do |t|
      t.string :callsign, null: false
      t.string :password_digest, null: false
      t.string :role, null: false, default: "user"
      t.timestamps
    end
    add_index :users, :callsign, unique: true
  end
end
