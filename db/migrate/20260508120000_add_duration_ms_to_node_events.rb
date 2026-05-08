class AddDurationMsToNodeEvents < ActiveRecord::Migration[8.1]
  def change
    add_column :node_events, :duration_ms, :integer
  end
end
