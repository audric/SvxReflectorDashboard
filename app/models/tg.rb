class Tg < ApplicationRecord
  validates :tg, presence: true, uniqueness: true
  validates :name, presence: true
  validates :kind, inclusion: { in: %w[local remote cluster] }

  scope :ordered, -> { order(:tg) }
  scope :local,   -> { where(kind: 'local') }
  scope :remote,  -> { where(kind: 'remote') }
  scope :cluster, -> { where(kind: 'cluster') }
end
