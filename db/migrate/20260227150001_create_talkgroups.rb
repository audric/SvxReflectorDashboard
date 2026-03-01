class CreateTalkgroups < ActiveRecord::Migration[7.1]
  def change
    create_table :talkgroups do |t|
      t.integer :number, null: false
      t.string :name

      t.timestamps
    end

    add_index :talkgroups, :number, unique: true
  end
end
