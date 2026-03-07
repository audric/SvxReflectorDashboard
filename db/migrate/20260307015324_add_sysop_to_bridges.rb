class AddSysopToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :sysop, :string
  end
end
