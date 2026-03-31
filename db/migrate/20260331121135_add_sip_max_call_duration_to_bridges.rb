class AddSipMaxCallDurationToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :sip_max_call_duration, :integer, default: 180
  end
end
