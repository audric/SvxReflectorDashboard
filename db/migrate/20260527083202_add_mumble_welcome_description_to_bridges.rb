class AddMumbleWelcomeDescriptionToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :mumble_welcome, :text
    add_column :bridges, :mumble_description, :text
  end
end
