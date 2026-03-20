class AddKindToTgs < ActiveRecord::Migration[7.1]
  def change
    add_column :tgs, :kind, :string, default: "local"
  end
end
