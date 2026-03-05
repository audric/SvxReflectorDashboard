class AddCwRogerBeepToUsers < ActiveRecord::Migration[8.1]
  def change
    add_column :users, :cw_roger_beep, :boolean, default: false, null: false
  end
end
