class AddApprovedToUsers < ActiveRecord::Migration[7.1]
  def change
    add_column :users, :approved, :boolean, default: false, null: false

    reversible do |dir|
      dir.up do
        User.update_all(approved: true)
      end
    end
  end
end
