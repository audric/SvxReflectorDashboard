module Admin
  class UsersController < ApplicationController
    before_action :require_admin
    before_action :set_user, only: %i[edit update destroy]

    def index
      @users = User.order(:callsign)
    end

    def new
      @user = User.new
    end

    def create
      @user = User.new(user_params)
      if @user.save
        redirect_to admin_users_path, notice: "User #{@user.callsign} created"
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit
    end

    def update
      attrs = user_params
      attrs = attrs.except(:password, :password_confirmation) if attrs[:password].blank?
      attrs = attrs.except(:role) if @user == current_user
      if @user.update(attrs)
        redirect_to admin_users_path, notice: "User #{@user.callsign} updated"
      else
        render :edit, status: :unprocessable_entity
      end
    end

    def destroy
      if @user == current_user
        redirect_to admin_users_path, alert: "You cannot delete yourself"
      else
        @user.destroy
        redirect_to admin_users_path, notice: "User #{@user.callsign} deleted"
      end
    end

    private

    def set_user
      @user = User.find(params[:id])
    end

    def user_params
      params.require(:user).permit(:callsign, :password, :password_confirmation, :role, :name, :email, :mobile, :telegram)
    end
  end
end
