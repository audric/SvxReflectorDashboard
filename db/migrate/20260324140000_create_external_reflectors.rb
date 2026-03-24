class CreateExternalReflectors < ActiveRecord::Migration[8.0]
  def change
    create_table :external_reflectors do |t|
      t.string :name, null: false
      t.string :status_url, null: false
      t.string :portal_url
      t.text :description
      t.boolean :enabled, default: true
      t.timestamps
    end
  end
end
