class CtcssTone < ApplicationRecord
  validates :frequency, presence: true, uniqueness: true
  validates :code, presence: true, uniqueness: true
end
