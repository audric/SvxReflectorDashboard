class AddCanTransmitToUsers < ActiveRecord::Migration[8.1]
  def change
    add_column :users, :can_transmit, :boolean, default: false, null: false
  end
end
