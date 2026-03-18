class AddXlxCallsignToBridges < ActiveRecord::Migration[7.1]
  def change
    add_column :bridges, :xlx_callsign, :string
    add_column :bridges, :xlx_callsign_suffix, :string
  end
end
