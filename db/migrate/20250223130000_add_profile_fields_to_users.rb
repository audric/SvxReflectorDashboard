class AddProfileFieldsToUsers < ActiveRecord::Migration[7.1]
  def change
    add_column :users, :name, :string
    add_column :users, :email, :string
    add_column :users, :mobile, :string
    add_column :users, :telegram, :string
  end
end
