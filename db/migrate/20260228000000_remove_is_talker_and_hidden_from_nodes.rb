class RemoveIsTalkerAndHiddenFromNodes < ActiveRecord::Migration[7.1]
  def change
    remove_column :nodes, :is_talker, :boolean, default: false
    remove_column :nodes, :hidden, :boolean, default: false
  end
end
