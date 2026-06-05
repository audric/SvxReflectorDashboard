class AddDmrProtocolToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :dmr_protocol, :string, default: "homebrew"
  end
end
