class Tg < ApplicationRecord
  validates :tg, presence: true, uniqueness: true
  validates :name, presence: true

  scope :ordered, -> { order(:tg) }
end
