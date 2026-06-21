class AddJanusUrlToBridges < ActiveRecord::Migration[7.1]
  def change
    add_column :bridges, :janus_url, :string
  end
end