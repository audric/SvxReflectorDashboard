class AddXlxFieldsToBridges < ActiveRecord::Migration[7.1]
  def change
    add_column :bridges, :xlx_host, :string
    add_column :bridges, :xlx_port, :integer
    add_column :bridges, :xlx_dmr_id, :integer
    add_column :bridges, :xlx_module, :string
  end
end
