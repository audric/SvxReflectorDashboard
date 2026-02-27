class SessionsController < ApplicationController
  layout false

  def new
  end

  def create
    user = User.find_by("UPPER(callsign) = ?", params[:callsign].to_s.upcase.strip)

    if user&.authenticate(params[:password])
      if user.approved?
        session[:user_id] = user.id
        redirect_to root_path, notice: "Logged in as #{user.callsign}"
      else
        flash.now[:alert] = "Your account is pending admin approval"
        render :new, status: :unprocessable_entity
      end
    else
      flash.now[:alert] = "Invalid callsign or password"
      render :new, status: :unprocessable_entity
    end
  end

  def destroy
    session.delete(:user_id)
    redirect_to root_path, notice: "Logged out"
  end
end
