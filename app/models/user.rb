class User < ApplicationRecord
  has_secure_password validations: false

  validates :password, presence: true, length: { minimum: 8 }, if: :password_required?
  validates :password, length: { minimum: 8 }, allow_blank: true, if: -> { password.present? }

  def oauth?
    provider.present?
  end

  before_validation { self.callsign = callsign.upcase.strip if callsign.present? }
  before_validation :manage_mumble_password
  after_save :sync_reflector_web_users, if: :reflector_auth_key_or_monitor_changed?
  after_destroy :sync_reflector_web_users
  after_save :sync_mumble_users, if: :mumble_relevant_changed?
  after_destroy :sync_mumble_users

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

  def allow_mumble?
    allow_mumble
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

  # Mint a Mumble key when access is granted, and wipe it when access is
  # revoked: a disabled user's old key must stop working. Clearing it also makes
  # mumble_relevant_changed? fire, so sync_mumble_users removes their Mumble
  # account + cert binding and restarts the server (kicking any live session).
  # Re-enabling mints a fresh key, so the old one can never be reused.
  def manage_mumble_password
    if allow_mumble
      self.mumble_password = SecureRandom.alphanumeric(20) if mumble_password.blank?
    elsif mumble_password.present?
      self.mumble_password = nil
    end
  end

  def mumble_relevant_changed?
    saved_change_to_allow_mumble? || saved_change_to_mumble_password? ||
      saved_change_to_callsign? || saved_change_to_role? || saved_change_to_can_transmit?
  end

  def sync_mumble_users
    MumbleSync.sync_users
  rescue => e
    Rails.logger.error "[User] Failed to sync mumble users: #{e.message}"
  end

  def password_required?
    !oauth? && (new_record? || password.present?)
  end

  def must_have_at_least_one_reflector_admin
    return unless reflector_admin_changed? && !reflector_admin
    remaining = User.where(reflector_admin: true).where.not(id: id).count
    errors.add(:reflector_admin, "cannot be removed — at least one reflector admin is required") if remaining.zero?
  end
end
