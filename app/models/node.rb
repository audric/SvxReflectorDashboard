class Node < ApplicationRecord
  belongs_to :node_class, optional: true
  belongs_to :talkgroup, optional: true
end
