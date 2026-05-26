class AccountController < ApplicationController
  layout false
  before_action :require_login

  def show
    @user = current_user
    @mumble_host = ENV.fetch("MUMBLE_PUBLIC_HOST", ENV.fetch("DOMAIN", "localhost"))
    @mumble_port = ENV.fetch("MUMBLE_PUBLIC_PORT", "64738")
  end

  def regenerate_mumble_token
    current_user.update(mumble_password: SecureRandom.alphanumeric(20)) if current_user.allow_mumble?
    redirect_to account_path, notice: "Mumble token regenerated."
  end
end
