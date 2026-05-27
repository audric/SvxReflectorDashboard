class AddMumbleBotUsernameToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :mumble_bot_username, :string
  end
end
