class AddDmrOptionsToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :dmr_options, :string
  end
end
