class AddSourceToNodeEvents < ActiveRecord::Migration[7.1]
  def change
    add_column :node_events, :source, :string
    add_index :node_events, :source
  end
end
