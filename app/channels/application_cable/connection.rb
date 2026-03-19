module ApplicationCable
  class Connection < ActionCable::Connection::Base
    identified_by :current_user

    def connect
      self.current_user = find_verified_user
    end

    private

    def find_verified_user
      user_id = request.session[:user_id]
      User.find_by(id: user_id) if user_id
      # Allow anonymous connections for UpdatesChannel (public dashboard)
      # Individual channels enforce their own permissions
    end
  end
end
