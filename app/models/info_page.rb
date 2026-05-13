class InfoPage < ApplicationRecord
  SLUG_FORMAT = /\A[a-z0-9]+(?:-[a-z0-9]+)*\z/

  validates :slug,  presence: true, uniqueness: { case_sensitive: false }, format: { with: SLUG_FORMAT, message: "must be lowercase letters/numbers separated by '-'" }
  validates :title, presence: true

  scope :published, -> { where(published: true) }
  scope :ordered,   -> { order(:position, :id) }

  before_validation :normalize_slug
  before_save       :assign_position, if: -> { position.blank? || position.zero? && new_record? }

  def to_param
    slug
  end

  private

  def normalize_slug
    self.slug = slug.to_s.strip.downcase.gsub(/\s+/, "-")
  end

  def assign_position
    self.position = (self.class.maximum(:position) || -1) + 1
  end
end
