class CreateInfoPages < ActiveRecord::Migration[8.1]
  def up
    create_table :info_pages do |t|
      t.string  :slug,      null: false
      t.string  :title,     null: false
      t.text    :body,      null: false, default: ""
      t.integer :position,  null: false, default: 0
      t.boolean :published, null: false, default: true
      t.timestamps
    end
    add_index :info_pages, :slug, unique: true
    add_index :info_pages, :position

    legacy = Setting.find_by(key: "system_description")
    if legacy && legacy.value.to_s.strip.length.positive?
      InfoPage.create!(
        slug:      "overview",
        title:     "Overview",
        body:      legacy.value,
        position:  0,
        published: true
      )
      legacy.destroy
    end
  end

  def down
    body = InfoPage.find_by(slug: "overview")&.body
    Setting.set("system_description", body) if body.present?
    drop_table :info_pages
  end
end
