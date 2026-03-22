class User < ApplicationRecord
  has_secure_password

  validates :password, length: { minimum: 8 }, if: -> { new_record? || password.present? }

  before_validation { self.callsign = callsign.upcase.strip if callsign.present? }
  after_save :sync_reflector_web_users, if: :reflector_auth_key_or_monitor_changed?
  after_destroy :sync_reflector_web_users

  validates :callsign, presence: true,
                      uniqueness: { case_sensitive: false },
                      length: { maximum: 8 },
                      format: { with: /\A[A-Z0-9]{1,3}\d[A-Z]{1,4}\z/, message: "is not a valid callsign (e.g. W1AW, KA1ABC, VE3XYZ)" }
  validates :role, inclusion: { in: %w[admin user] }

  validate :must_have_at_least_one_reflector_admin

  def admin?
    role == "admin"
  end

  def reflector_admin?
    reflector_admin
  end

  def user?
    role == "user"
  end

  def can_monitor?
    can_monitor
  end

  def can_transmit?
    can_transmit
  end

  def cw_roger_beep?
    cw_roger_beep
  end

  private

  def reflector_auth_key_or_monitor_changed?
    saved_change_to_reflector_auth_key? || saved_change_to_can_monitor? || saved_change_to_callsign?
  end

  def sync_reflector_web_users
    ReflectorConfig.sync_web_users
  rescue => e
    Rails.logger.error "[User] Failed to sync reflector web users: #{e.message}"
  end

  def must_have_at_least_one_reflector_admin
    return unless reflector_admin_changed? && !reflector_admin
    remaining = User.where(reflector_admin: true).where.not(id: id).count
    errors.add(:reflector_admin, "cannot be removed — at least one reflector admin is required") if remaining.zero?
  end
end
