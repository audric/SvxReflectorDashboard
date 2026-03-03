class AddReflectorAuthKeyToUsers < ActiveRecord::Migration[8.1]
  def change
    add_column :users, :reflector_auth_key, :string
  end
end
