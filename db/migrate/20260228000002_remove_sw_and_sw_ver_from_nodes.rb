class RemoveSwAndSwVerFromNodes < ActiveRecord::Migration[7.1]
  def change
    remove_column :nodes, :sw, :string
    remove_column :nodes, :sw_ver, :string
  end
end
