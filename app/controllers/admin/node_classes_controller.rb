module Admin
  class NodeClassesController < ApplicationController
    layout false
    before_action :require_admin
    before_action :set_node_class, only: %i[edit update destroy]

    def index
      @node_classes = NodeClass.order(:name)
    end

    def new
      @node_class = NodeClass.new
    end

    def create
      @node_class = NodeClass.new(node_class_params)
      if @node_class.save
        redirect_to admin_node_classes_path, notice: "Class #{@node_class.name} created"
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit
    end

    def update
      if @node_class.update(node_class_params)
        redirect_to admin_node_classes_path, notice: "Class #{@node_class.name} updated"
      else
        render :edit, status: :unprocessable_entity
      end
    end

    def destroy
      @node_class.destroy
      redirect_to admin_node_classes_path, notice: "Class #{@node_class.name} deleted"
    end

    private

    def set_node_class
      @node_class = NodeClass.find(params[:id])
    end

    def node_class_params
      params.require(:node_class).permit(:name)
    end
  end
end
