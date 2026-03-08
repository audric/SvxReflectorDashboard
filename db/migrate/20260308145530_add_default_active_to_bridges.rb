class AddDefaultActiveToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :default_active, :boolean, default: true
  end
end
