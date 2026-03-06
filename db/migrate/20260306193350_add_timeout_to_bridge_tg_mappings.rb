class AddTimeoutToBridgeTgMappings < ActiveRecord::Migration[8.1]
  def change
    add_column :bridge_tg_mappings, :timeout, :integer
  end
end
