class AddSipPttToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :sip_vox_timeout, :integer, default: 3
    add_column :bridges, :sip_ptt_key, :string, default: "*"
  end
end
