class AddActivateOnActivityToBridgeTgMappings < ActiveRecord::Migration[8.1]
  def change
    add_column :bridge_tg_mappings, :activate_on_activity, :string
  end
end
