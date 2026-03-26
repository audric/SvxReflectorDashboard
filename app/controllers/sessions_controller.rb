class SessionsController < ApplicationController
  layout false
  skip_forgery_protection only: :destroy

  MAX_ATTEMPTS = 5
  LOCKOUT_PERIOD = 5.minutes

  def new
  end

  def create
    if login_locked?
      flash.now[:alert] = "Too many failed attempts. Try again in a few minutes."
      render :new, status: :too_many_requests
      return
    end

    user = User.find_by("UPPER(callsign) = ?", params[:callsign].to_s.upcase.strip)

    if user&.authenticate(params[:password])
      if user.approved?
        clear_login_attempts
        reset_session
        user.update_column(:last_sign_in_at, Time.current)
        session[:user_id] = user.id
        redirect_to root_path, notice: "Logged in as #{user.callsign}"
      else
        flash.now[:alert] = "Your account is pending admin approval"
        render :new, status: :unprocessable_entity
      end
    else
      record_failed_attempt
      flash.now[:alert] = "Invalid callsign or password"
      render :new, status: :unprocessable_entity
    end
  end

  def destroy
    reset_session
    redirect_to login_path
  end

  private

  def throttle_key
    "login_attempts:#{request.remote_ip}"
  end

  def login_locked?
    attempts = Rails.cache.read(throttle_key).to_i
    attempts >= MAX_ATTEMPTS
  end

  def record_failed_attempt
    count = Rails.cache.read(throttle_key).to_i + 1
    Rails.cache.write(throttle_key, count, expires_in: LOCKOUT_PERIOD)
  end

  def clear_login_attempts
    Rails.cache.delete(throttle_key)
  end
end
