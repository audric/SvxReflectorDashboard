class AddIaxFieldsToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :iax_username, :string
    add_column :bridges, :iax_password, :string
    add_column :bridges, :iax_server, :string
    add_column :bridges, :iax_port, :integer, default: 4569
    add_column :bridges, :iax_extension, :string
    add_column :bridges, :iax_context, :string, default: "friend"
    add_column :bridges, :iax_mode, :string, default: "persistent"
    add_column :bridges, :iax_idle_timeout, :integer, default: 30
    add_column :bridges, :iax_codecs, :string, default: "gsm,ulaw,alaw,g726"
  end
end
