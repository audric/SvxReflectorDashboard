class AddNodeClassAndTalkgroupRefsToNodes < ActiveRecord::Migration[7.1]
  def change
    remove_column :nodes, :node_class, :string
    remove_column :nodes, :tg, :integer
    add_reference :nodes, :node_class, foreign_key: true, null: true
    add_reference :nodes, :talkgroup, foreign_key: true, null: true
  end
end
