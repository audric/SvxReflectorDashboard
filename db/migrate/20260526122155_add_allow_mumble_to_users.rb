class AddAllowMumbleToUsers < ActiveRecord::Migration[8.1]
  def change
    add_column :users, :allow_mumble, :boolean, default: false, null: false
    add_column :users, :mumble_password, :string
  end
end
