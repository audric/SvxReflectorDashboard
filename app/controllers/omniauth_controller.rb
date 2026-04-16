class OmniauthController < ApplicationController
  layout false

  # POST /auth/google_oauth2/callback — Google redirects here after consent
  def google
    auth = request.env['omniauth.auth']
    unless auth
      redirect_to login_path, alert: "Google authentication failed."
      return
    end

    provider = auth['provider']
    uid      = auth['uid']
    email    = auth.dig('info', 'email')
    name     = auth.dig('info', 'name')

    # 1) Existing user with this provider+uid — log them in
    user = User.find_by(provider: provider, uid: uid)
    if user
      return login_user(user)
    end

    # 2) Existing user with matching email — link the OAuth identity and log in
    if email.present?
      user = User.find_by(email: email)
      if user
        user.update_columns(provider: provider, uid: uid)
        return login_user(user)
      end
    end

    # 3) New user — store OAuth data in session, redirect to callsign completion
    session[:oauth] = { provider: provider, uid: uid, email: email, name: name }
    redirect_to auth_complete_path
  end

  # GET /auth/failure — OmniAuth error callback
  def failure
    redirect_to login_path, alert: "Google authentication failed: #{params[:message]}"
  end

  # GET /auth/complete — show callsign completion form
  def complete
    unless session[:oauth]
      redirect_to register_path
      return
    end
    @oauth = session[:oauth]
  end

  # POST /auth/complete — create account with callsign
  def finalize
    oauth = session[:oauth]
    unless oauth
      redirect_to register_path, alert: "Session expired. Please try again."
      return
    end

    @oauth = oauth
    @user = User.new(
      callsign: params[:callsign].to_s.strip,
      name: oauth['name'],
      email: oauth['email'],
      provider: oauth['provider'],
      uid: oauth['uid'],
      role: 'user',
      approved: false
    )

    if @user.save
      session.delete(:oauth)
      redirect_to login_path, notice: "Account created. An admin will review your registration."
    else
      render :complete, status: :unprocessable_entity
    end
  end

  private

  def login_user(user)
    unless user.approved?
      redirect_to login_path, alert: "Your account is pending admin approval."
      return
    end
    reset_session
    user.update_column(:last_sign_in_at, Time.current)
    session[:user_id] = user.id
    redirect_to root_path, notice: "Logged in as #{user.callsign}"
  end
end
