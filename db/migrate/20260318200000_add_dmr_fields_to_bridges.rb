class AddDmrFieldsToBridges < ActiveRecord::Migration[7.1]
  def change
    add_column :bridges, :dmr_host, :string
    add_column :bridges, :dmr_port, :integer
    add_column :bridges, :dmr_id, :integer
    add_column :bridges, :dmr_password, :string
    add_column :bridges, :dmr_talkgroup, :integer
    add_column :bridges, :dmr_timeslot, :integer
    add_column :bridges, :dmr_color_code, :integer
    add_column :bridges, :dmr_callsign, :string
  end
end
