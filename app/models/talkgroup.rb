class Talkgroup < ApplicationRecord
  validates :number, presence: true, uniqueness: true
end
