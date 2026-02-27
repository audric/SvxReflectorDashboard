class User < ApplicationRecord
  has_secure_password

  validates :password, length: { minimum: 8 }, if: -> { new_record? || password.present? }

  before_validation { self.callsign = callsign.upcase.strip if callsign.present? }

  validates :callsign, presence: true, uniqueness: { case_sensitive: false }
  validates :role, inclusion: { in: %w[admin user] }

  def admin?
    role == "admin"
  end

  def user?
    role == "user"
  end
end
