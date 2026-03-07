class AddNodeLocationToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :node_location, :string
  end
end
