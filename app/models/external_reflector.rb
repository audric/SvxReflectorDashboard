class ExternalReflector < ApplicationRecord
  validates :name, presence: true, uniqueness: true
  validates :status_url, presence: true, format: { with: /\Ahttps?:\/\//i, message: "must start with http:// or https://" }

  scope :enabled, -> { where(enabled: true) }
  scope :ordered, -> { order(:name) }
end
