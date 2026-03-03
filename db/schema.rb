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

ActiveRecord::Schema[8.1].define(version: 2026_03_03_000000) do
  create_table "ctcss_tones", force: :cascade do |t|
    t.string "code", null: false
    t.datetime "created_at", null: false
    t.decimal "frequency", precision: 5, scale: 1, null: false
    t.datetime "updated_at", null: false
    t.index ["code"], name: "index_ctcss_tones_on_code", unique: true
    t.index ["frequency"], name: "index_ctcss_tones_on_frequency", unique: true
  end

  create_table "node_classes", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "name", null: false
    t.datetime "updated_at", null: false
    t.index ["name"], name: "index_node_classes_on_name", unique: true
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

  create_table "node_infos", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "grid_locator"
    t.boolean "hidden", default: false
    t.float "latitude"
    t.float "longitude"
    t.string "node_class"
    t.string "node_location"
    t.string "qth_name"
    t.string "rx_ant_comment"
    t.integer "rx_ant_dir"
    t.integer "rx_ant_height"
    t.string "rx_ctcss_freqs"
    t.float "rx_freq"
    t.string "rx_name"
    t.string "rx_sql_type"
    t.string "sysop"
    t.text "tone_to_talkgroup"
    t.string "tx_ant_comment"
    t.integer "tx_ant_dir"
    t.integer "tx_ant_height"
    t.float "tx_ctcss_freq"
    t.float "tx_freq"
    t.string "tx_name"
    t.float "tx_pwr"
    t.datetime "updated_at", null: false
    t.integer "user_id", null: false
    t.index ["user_id"], name: "index_node_infos_on_user_id", unique: true
  end

  create_table "nodes", force: :cascade do |t|
    t.string "callsign", null: false
    t.datetime "created_at", null: false
    t.string "locator"
    t.text "monitored_tgs"
    t.integer "node_class_id"
    t.string "node_location"
    t.string "rx_freq"
    t.string "sysop"
    t.integer "talkgroup_id"
    t.text "tone_to_talkgroup"
    t.string "tx_freq"
    t.datetime "updated_at", null: false
    t.index ["callsign"], name: "index_nodes_on_callsign", unique: true
    t.index ["node_class_id"], name: "index_nodes_on_node_class_id"
    t.index ["talkgroup_id"], name: "index_nodes_on_talkgroup_id"
  end

  create_table "settings", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "key", null: false
    t.datetime "updated_at", null: false
    t.string "value"
    t.index ["key"], name: "index_settings_on_key", unique: true
  end

  create_table "talkgroups", force: :cascade do |t|
    t.datetime "created_at", null: false
    t.string "name"
    t.integer "number", null: false
    t.datetime "updated_at", null: false
    t.index ["number"], name: "index_talkgroups_on_number", unique: true
  end

  create_table "users", force: :cascade do |t|
    t.boolean "approved", default: false, null: false
    t.string "callsign", null: false
    t.boolean "can_monitor", default: false, null: false
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

  add_foreign_key "node_infos", "users"
  add_foreign_key "nodes", "node_classes"
  add_foreign_key "nodes", "talkgroups"
end
