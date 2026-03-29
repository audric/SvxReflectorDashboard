class AddPollToExternalReflectors < ActiveRecord::Migration[8.0]
  def change
    add_column :external_reflectors, :poll, :boolean, default: false
  end
end
