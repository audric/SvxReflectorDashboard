class User < ApplicationRecord
  has_secure_password

  validates :password, length: { minimum: 8 }, if: -> { new_record? || password.present? }

  before_validation { self.callsign = callsign.upcase.strip if callsign.present? }

  validates :callsign, presence: true,
                      uniqueness: { case_sensitive: false },
                      length: { maximum: 8 },
                      format: { with: /\A[A-Z0-9]{1,3}\d[A-Z]{1,4}\z/, message: "is not a valid callsign (e.g. W1AW, KA1ABC, VE3XYZ)" }
  validates :role, inclusion: { in: %w[admin user] }

  def admin?
    role == "admin"
  end

  def user?
    role == "user"
  end
end
