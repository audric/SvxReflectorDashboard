class ApplicationController < ActionController::Base
  helper_method :current_user, :logged_in?, :reflector_mode, :google_oauth_enabled?, :plain_registration_enabled?
  before_action :set_brand_name

  private

  def set_brand_name
    @reflector_host = Setting.get('brand_name', ENV.fetch('BRAND_NAME', ''))
  end

  def current_user
    @current_user ||= User.find_by(id: session[:user_id]) if session[:user_id]
  end

  def logged_in?
    current_user.present?
  end

  def require_login
    unless logged_in?
      redirect_to login_path, alert: "Please log in"
    end
  end

  def reflector_mode
    @_reflector_mode ||= begin
      redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
      config = JSON.parse(redis.get('reflector:config') || '{}')
      config['mode'] || 'reflector'
    rescue
      'reflector'
    end
  end

  def google_oauth_enabled?
    ENV['GOOGLE_CLIENT_ID'].present? && ENV['GOOGLE_CLIENT_SECRET'].present?
  end

  # Plain (password) registration form. Env var sets the default; an admin can
  # override it at runtime via the Settings tab (Setting key 'plain_registration').
  def plain_registration_enabled?
    val = Setting.get('plain_registration', ENV.fetch('PLAIN_REGISTRATION', 'true'))
    ActiveModel::Type::Boolean.new.cast(val)
  end

  def require_admin
    require_login
    return if performed?
    unless current_user.admin?
      redirect_to root_path, alert: "Admin access required"
    end
  end
end
