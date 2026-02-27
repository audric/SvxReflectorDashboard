# This file is auto-generated from the current state of the database. Instead
# of editing this file, please use the migrations feature of Active Record to
# incrementally modify your database, and then regenerate this schema definition.
#
# This file is the source Rails uses to define your schema when running `bin/rails
# db:schema:load`. When creating a new database, `bin/rails db:schema:load` tends to
# be faster and is potentially less error prone than running all of your
# migrations from scratch. Old migrations may fail to apply correctly if those
# migrations use external dependencies or application code.
#
# It's strongly recommended that you check this file into your version control system.

ActiveRecord::Schema[8.1].define(version: 2026_02_27_120000) do
  create_table "ctcss_tones", force: :cascade do |t|
    t.string "code", null: false
    t.datetime "created_at", null: false
    t.decimal "frequency", precision: 5, scale: 1, null: false
    t.datetime "updated_at", null: false
    t.index ["code"], name: "index_ctcss_tones_on_code", unique: true
    t.index ["frequency"], name: "index_ctcss_tones_on_frequency", unique: true
  end

  create_table "node_events", force: :cascade do |t|
    t.string "callsign", null: false
    t.datetime "created_at", null: false
    t.string "event_type", null: false
    t.string "node_class"
    t.string "node_location"
    t.integer "tg"
    t.datetime "updated_at", null: false
    t.index ["callsign"], name: "index_node_events_on_callsign"
    t.index ["created_at"], name: "index_node_events_on_created_at"
    t.index ["event_type"], name: "index_node_events_on_event_type"
    t.index ["tg"], name: "index_node_events_on_tg"
  end

  create_table "settings", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "key", null: false
    t.datetime "updated_at", null: false
    t.string "value"
    t.index ["key"], name: "index_settings_on_key", unique: true
  end

  create_table "users", force: :cascade do |t|
    t.string "callsign", null: false
    t.datetime "created_at", null: false
    t.string "email"
    t.string "mobile"
    t.string "name"
    t.string "password_digest", null: false
    t.string "role", default: "user", null: false
    t.string "telegram"
    t.datetime "updated_at", null: false
    t.index ["callsign"], name: "index_users_on_callsign", unique: true
  end
end
