class AddXlxProtocolToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :xlx_protocol, :string, default: "DCS"
  end
end
