class CreateTgs < ActiveRecord::Migration[8.1]
  def change
    create_table :tgs do |t|
      t.string :tg
      t.string :name
      t.text :description

      t.timestamps
    end
  end
end
