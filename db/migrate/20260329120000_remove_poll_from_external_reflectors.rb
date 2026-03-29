class RemovePollFromExternalReflectors < ActiveRecord::Migration[8.0]
  def change
    remove_column :external_reflectors, :poll, :boolean, default: false
  end
end
