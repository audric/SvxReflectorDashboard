class AddSipPinToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :sip_pin, :string
    add_column :bridges, :sip_pin_timeout, :integer, default: 10
  end
end
