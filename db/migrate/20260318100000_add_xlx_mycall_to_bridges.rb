class AddXlxMycallToBridges < ActiveRecord::Migration[7.1]
  def change
    add_column :bridges, :xlx_mycall, :string
    add_column :bridges, :xlx_mycall_suffix, :string
    add_column :bridges, :xlx_reflector_name, :string
  end
end
