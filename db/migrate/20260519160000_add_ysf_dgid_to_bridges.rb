class AddYsfDgidToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :ysf_dgid, :integer, default: 0
  end
end
