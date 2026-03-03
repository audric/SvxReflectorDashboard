class AddCanMonitorToUsers < ActiveRecord::Migration[8.1]
  def change
    add_column :users, :can_monitor, :boolean, default: false, null: false
  end
end
