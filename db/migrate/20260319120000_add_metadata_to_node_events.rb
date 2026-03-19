class AddMetadataToNodeEvents < ActiveRecord::Migration[7.1]
  def change
    add_column :node_events, :metadata, :text
  end
end
