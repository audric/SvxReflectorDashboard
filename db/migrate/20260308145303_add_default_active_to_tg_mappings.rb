class AddDefaultActiveToTgMappings < ActiveRecord::Migration[8.1]
  def change
    add_column :bridge_tg_mappings, :default_active, :boolean, default: true
  end
end
