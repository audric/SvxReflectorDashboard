class RegistrationsController < ApplicationController
  layout false

  def new
    @user = User.new
  end

  def create
    @user = User.new(registration_params.merge(role: "user", approved: false))
    if @user.save
      redirect_to login_path, notice: "Account created. An admin will review your registration."
    else
      render :new, status: :unprocessable_entity
    end
  end

  private

  def registration_params
    params.require(:user).permit(:callsign, :password, :password_confirmation, :name, :email, :mobile, :telegram)
  end
end
