class AddReflectorAdminToUsers < ActiveRecord::Migration[7.1]
  def up
    add_column :users, :reflector_admin, :boolean, default: false, null: false
    # Make all existing admin users reflector admins
    execute "UPDATE users SET reflector_admin = 1 WHERE role = 'admin'"
  end

  def down
    remove_column :users, :reflector_admin
  end
end
