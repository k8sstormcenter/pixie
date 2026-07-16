# __generated__ by Terraform
# Please review these resources and move them into your main configuration files.

# __generated__ by Terraform from "captcha::en"
resource "auth0_prompt_custom_text" "en_captcha" {
  body     = jsonencode({})
  language = "en"
  prompt   = "captcha"
}

# __generated__ by Terraform from "customized-consent::en"
resource "auth0_prompt_custom_text" "en_customized_consent" {
  body     = jsonencode({})
  language = "en"
  prompt   = "customized-consent"
}

# __generated__ by Terraform from "mfa-push::en"
resource "auth0_prompt_custom_text" "en_mfa_push" {
  body     = jsonencode({})
  language = "en"
  prompt   = "mfa-push"
}

# __generated__ by Terraform from "login-password::en"
resource "auth0_prompt_custom_text" "en_login_password" {
  body     = jsonencode({})
  language = "en"
  prompt   = "login-password"
}

# __generated__ by Terraform from "con_3HhKvUUeZr2ijQ90"
resource "auth0_connection" "username_password_authentication" {
  is_domain_connection = false
  metadata             = {}
  name                 = "Username-Password-Authentication"
  realms               = ["Username-Password-Authentication"]
  show_as_button       = null
  strategy             = "auth0"
  authentication {
    active = true
  }
  connected_accounts {
    active = false
  }
  options {
    access_token_url                       = null
    adfs_server                            = null
    allowed_audiences                      = []
    api_enable_users                       = false
    app_id                                 = null
    auth_params                            = {}
    brute_force_protection                 = true
    client_id                              = null
    client_secret                          = null # sensitive
    community_base_url                     = null
    configuration                          = null # sensitive
    consumer_key                           = null
    consumer_secret                        = null
    custom_scripts                         = {}
    debug                                  = false
    digest_algorithm                       = null
    disable_cache                          = false
    disable_self_service_change_password   = false
    disable_sign_out                       = false
    disable_signup                         = false
    discovery_url                          = null
    domain                                 = null
    domain_aliases                         = []
    email                                  = false
    enable_script_context                  = false
    enabled_database_customization         = false
    entity_id                              = null
    fed_metadata_xml                       = null
    fields_map                             = null
    forward_request_info                   = false
    from                                   = null
    gateway_url                            = null
    global_token_revocation_jwt_iss        = null
    global_token_revocation_jwt_sub        = null
    icon_url                               = null
    identity_api                           = null
    import_mode                            = false
    ips                                    = []
    key_id                                 = null
    map_user_id_to_id                      = false
    max_groups_to_retrieve                 = null
    messaging_service_sid                  = null
    metadata_url                           = null
    metadata_xml                           = null
    name                                   = null
    non_persistent_attrs                   = []
    password_policy                        = "good"
    ping_federate_base_url                 = null
    pkce_enabled                           = false
    precedence                             = []
    protocol_binding                       = null
    provider                               = null
    realm_fallback                         = false
    request_template                       = null
    request_token_url                      = null
    requires_username                      = false
    scopes                                 = []
    scripts                                = {}
    send_back_channel_nonce                = false
    session_key                            = null
    should_trust_email_verified_connection = null
    sign_saml_request                      = false
    signature_algorithm                    = null
    signature_method                       = null
    strategy_version                       = 2
    subject                                = null
    syntax                                 = null
    team_id                                = null
    template                               = null
    tenant_domain                          = null
    token_endpoint_auth_method             = null
    token_endpoint_auth_signing_alg        = null
    twilio_sid                             = null
    twilio_token                           = null # sensitive
    type                                   = null
    upstream_params                        = null
    use_cert_auth                          = false
    use_kerberos                           = false
    use_oauth_spec_scope                   = false
    use_wsfed                              = false
    user_authorization_url                 = null
    user_id_attribute                      = null
    waad_common_endpoint                   = false
    waad_protocol                          = null
    authentication_methods {
      passkey {
        enabled = false
      }
      password {
        enabled = true
      }
    }
    mfa {
      active                 = true
      return_enroll_settings = true
    }
    passkey_options {
      challenge_ui                   = "both"
      local_enrollment_enabled       = true
      progressive_enrollment_enabled = true
    }
  }
}

# __generated__ by Terraform from "brute-force-protection::en"
resource "auth0_prompt_custom_text" "en_brute_force_protection" {
  body     = jsonencode({})
  language = "en"
  prompt   = "brute-force-protection"
}

# __generated__ by Terraform from "signup-id:signup-id"
resource "auth0_prompt_screen_partial" "signup_id_signup_id" {
  prompt_type = "signup-id"
  screen_name = "signup-id"
}

# __generated__ by Terraform from "con_gq4skrvMx0vH9Ocv"
resource "auth0_connection" "google_oauth2" {
  is_domain_connection = false
  metadata             = {}
  name                 = "google-oauth2"
  realms               = ["google-oauth2"]
  show_as_button       = null
  strategy             = "google-oauth2"
  authentication {
    active = true
  }
  connected_accounts {
    active = false
  }
  options {
    access_token_url                       = null
    adfs_server                            = null
    allowed_audiences                      = []
    api_enable_users                       = false
    app_id                                 = null
    auth_params                            = {}
    brute_force_protection                 = false
    client_id                              = null
    client_secret                          = null # sensitive
    community_base_url                     = null
    configuration                          = null # sensitive
    consumer_key                           = null
    consumer_secret                        = null
    custom_scripts                         = {}
    debug                                  = false
    digest_algorithm                       = null
    disable_cache                          = false
    disable_self_service_change_password   = false
    disable_sign_out                       = false
    disable_signup                         = false
    discovery_url                          = null
    domain                                 = null
    domain_aliases                         = []
    email                                  = false
    enable_script_context                  = false
    enabled_database_customization         = false
    entity_id                              = null
    fed_metadata_xml                       = null
    fields_map                             = null
    forward_request_info                   = false
    from                                   = null
    gateway_url                            = null
    global_token_revocation_jwt_iss        = null
    global_token_revocation_jwt_sub        = null
    icon_url                               = null
    identity_api                           = null
    import_mode                            = false
    ips                                    = []
    key_id                                 = null
    map_user_id_to_id                      = false
    max_groups_to_retrieve                 = null
    messaging_service_sid                  = null
    metadata_url                           = null
    metadata_xml                           = null
    name                                   = null
    non_persistent_attrs                   = []
    ping_federate_base_url                 = null
    pkce_enabled                           = false
    precedence                             = []
    protocol_binding                       = null
    provider                               = null
    realm_fallback                         = false
    request_template                       = null
    request_token_url                      = null
    requires_username                      = false
    scopes                                 = ["email", "profile"]
    scripts                                = {}
    send_back_channel_nonce                = false
    session_key                            = null
    should_trust_email_verified_connection = null
    sign_saml_request                      = false
    signature_algorithm                    = null
    signature_method                       = null
    strategy_version                       = 0
    subject                                = null
    syntax                                 = null
    team_id                                = null
    template                               = null
    tenant_domain                          = null
    token_endpoint_auth_method             = null
    token_endpoint_auth_signing_alg        = null
    twilio_sid                             = null
    twilio_token                           = null # sensitive
    type                                   = null
    upstream_params                        = null
    use_cert_auth                          = false
    use_kerberos                           = false
    use_oauth_spec_scope                   = false
    use_wsfed                              = false
    user_authorization_url                 = null
    user_id_attribute                      = null
    waad_common_endpoint                   = false
    waad_protocol                          = null
  }
}

# __generated__ by Terraform from "passkeys::en"
resource "auth0_prompt_custom_text" "en_passkeys" {
  body     = jsonencode({})
  language = "en"
  prompt   = "passkeys"
}

# __generated__ by Terraform from "mfa-webauthn::en"
resource "auth0_prompt_custom_text" "en_mfa_webauthn" {
  body     = jsonencode({})
  language = "en"
  prompt   = "mfa-webauthn"
}

# __generated__ by Terraform from "login-passwordless:login-passwordless-email-code"
resource "auth0_prompt_screen_partial" "login_passwordless_login_passwordless_email_code" {
  prompt_type = "login-passwordless"
  screen_name = "login-passwordless-email-code"
}

# __generated__ by Terraform from "2fab16a6-8ff1-462b-b109-7233fc6acb35"
resource "auth0_pages" "pages" {
  login {
    enabled = false
    html    = ""
  }
}

# __generated__ by Terraform from "login-email-verification::en"
resource "auth0_prompt_custom_text" "en_login_email_verification" {
  body     = jsonencode({})
  language = "en"
  prompt   = "login-email-verification"
}

# __generated__ by Terraform from "phone-identifier-challenge::en"
resource "auth0_prompt_custom_text" "en_phone_identifier_challenge" {
  body     = jsonencode({})
  language = "en"
  prompt   = "phone-identifier-challenge"
}

# __generated__ by Terraform from "con_gq4skrvMx0vH9Ocv"
resource "auth0_connection_clients" "google_oauth2" {
  connection_id   = "con_gq4skrvMx0vH9Ocv"
  enabled_clients = ["0DxLenl4KRr8g0fxmxsh7SjQfb7unbMe", "5akM0SumJG95tcRlP390pXzJoZkkYNDT"]
}

# __generated__ by Terraform from "mfa::en"
resource "auth0_prompt_custom_text" "en_mfa" {
  body     = jsonencode({})
  language = "en"
  prompt   = "mfa"
}

# __generated__ by Terraform from "mfa-email::en"
resource "auth0_prompt_custom_text" "en_mfa_email" {
  body     = jsonencode({})
  language = "en"
  prompt   = "mfa-email"
}

# __generated__ by Terraform from "customized-consent:customized-consent"
resource "auth0_prompt_screen_partial" "customized_consent_customized_consent" {
  prompt_type = "customized-consent"
  screen_name = "customized-consent"
}

# __generated__ by Terraform from "login-passwordless::en"
resource "auth0_prompt_custom_text" "en_login_passwordless" {
  body     = jsonencode({})
  language = "en"
  prompt   = "login-passwordless"
}

# __generated__ by Terraform from "email-identifier-challenge::en"
resource "auth0_prompt_custom_text" "en_email_identifier_challenge" {
  body     = jsonencode({})
  language = "en"
  prompt   = "email-identifier-challenge"
}

# __generated__ by Terraform from "common::en"
resource "auth0_prompt_custom_text" "en_common" {
  body     = jsonencode({})
  language = "en"
  prompt   = "common"
}

# __generated__ by Terraform from "signup-password:signup-password"
resource "auth0_prompt_screen_partial" "signup_password_signup_password" {
  prompt_type = "signup-password"
  screen_name = "signup-password"
}

# __generated__ by Terraform from "96f40177-8ba2-4f05-aa75-3c6e3c824cb2"
resource "auth0_tenant" "tenant" {
  acr_values_supported                                 = []
  allow_organization_name_in_authentication_api        = false
  allowed_logout_urls                                  = []
  customize_mfa_in_postlogin_action                    = false
  disable_acr_values_supported                         = true
  enabled_locales                                      = ["en"]
  ephemeral_session_lifetime                           = 168
  idle_ephemeral_session_lifetime                      = 72
  idle_session_lifetime                                = 72
  pushed_authorization_requests_supported              = false
  sandbox_version                                      = "22"
  session_lifetime                                     = 168
  skip_non_verifiable_callback_uri_confirmation_prompt = jsonencode(null)
  flags {
    allow_legacy_delegation_grant_types    = false
    allow_legacy_ro_grant_types            = false
    allow_legacy_tokeninfo_endpoint        = false
    dashboard_insights_view                = false
    dashboard_log_streams_next             = false
    disable_clickjack_protection_headers   = false
    disable_fields_map_fix                 = false
    disable_management_api_sms_obfuscation = false
    enable_adfs_waad_email_verification    = false
    enable_apis_section                    = false
    enable_client_connections              = false
    enable_custom_domain_in_emails         = false
    enable_dynamic_client_registration     = false
    enable_idtoken_api2                    = false
    enable_legacy_logs_search_v2           = false
    enable_legacy_profile                  = false
    enable_pipeline2                       = false
    enable_public_signup_user_exists_error = false
    enable_sso                             = true
    mfa_show_factor_list_on_enrollment     = false
    no_disclose_enterprise_connections     = false
    remove_alg_from_jwks                   = false
    revoke_refresh_token_grant             = false
    use_scope_descriptions_for_consent     = false
  }
  mtls {
    disable                 = true
    enable_endpoint_aliases = false
  }
  oidc_logout {
    rp_logout_end_session_endpoint_discovery = true
  }
  session_cookie {
    mode = null
  }
  sessions {
    oidc_logout_prompt_enabled = false
  }
}

# __generated__ by Terraform from "18c8f86c-568f-489b-9eb7-5e7f91159985"
resource "auth0_guardian" "guardian" {
  email         = false
  otp           = false
  policy        = "never"
  recovery_code = false
  duo {
    enabled         = false
    hostname        = null
    integration_key = null
    secret_key      = null # sensitive
  }
  phone {
    enabled       = false
    message_types = []
    provider      = null
  }
  push {
    enabled  = false
    provider = null
  }
  webauthn_platform {
    enabled                = false
    override_relying_party = false
  }
  webauthn_roaming {
    enabled                = false
    override_relying_party = false
  }
}

# __generated__ by Terraform from "device-flow::en"
resource "auth0_prompt_custom_text" "en_device_flow" {
  body     = jsonencode({})
  language = "en"
  prompt   = "device-flow"
}

# __generated__ by Terraform from "login-password:login-password"
resource "auth0_prompt_screen_partial" "login_password_login_password" {
  prompt_type = "login-password"
  screen_name = "login-password"
}

# __generated__ by Terraform from "mfa-phone::en"
resource "auth0_prompt_custom_text" "en_mfa_phone" {
  body     = jsonencode({})
  language = "en"
  prompt   = "mfa-phone"
}

# __generated__ by Terraform from "cgr_lgvjTwTNm53mojKq"
resource "auth0_client_grant" "akm0sumjg95tcrlp390pxzjozkkyndt_https_k8sstormcenter_eu_auth0_com_api_v2" {
  allow_all_scopes            = false
  allow_any_organization      = false
  audience                    = "https://k8sstormcenter.eu.auth0.com/api/v2/"
  authorization_details_types = []
  client_id                   = "5akM0SumJG95tcRlP390pXzJoZkkYNDT"
  organization_usage          = null
  scopes                      = ["read:users", "update:users", "delete:users", "create:users", "read:users_app_metadata", "update:users_app_metadata", "delete:users_app_metadata", "create:users_app_metadata", "read:user_custom_blocks", "create:user_custom_blocks", "delete:user_custom_blocks", "create:user_tickets", "read:logs_users", "read:user_idp_tokens", "create:user_attribute_profiles", "read:user_attribute_profiles", "update:user_attribute_profiles", "delete:user_attribute_profiles", "read:user_effective_permissions", "read:user_effective_roles", "read:user_role_source_groups", "read:user_permission_source_roles"]
  subject_type                = "client"
}

# __generated__ by Terraform from "login-passwordless:login-passwordless-sms-otp"
resource "auth0_prompt_screen_partial" "login_passwordless_login_passwordless_sms_otp" {
  prompt_type = "login-passwordless"
  screen_name = "login-passwordless-sms-otp"
}

# __generated__ by Terraform from "login-id::en"
resource "auth0_prompt_custom_text" "en_login_id" {
  body     = jsonencode({})
  language = "en"
  prompt   = "login-id"
}

# __generated__ by Terraform from "organizations::en"
resource "auth0_prompt_custom_text" "en_organizations" {
  body     = jsonencode({})
  language = "en"
  prompt   = "organizations"
}

# __generated__ by Terraform from "consent::en"
resource "auth0_prompt_custom_text" "en_consent" {
  body     = jsonencode({})
  language = "en"
  prompt   = "consent"
}

# __generated__ by Terraform from "6abbc880-c36f-4503-a368-f35ad65aee3b"
resource "auth0_prompt" "prompts" {
  identifier_first               = false
  universal_login_experience     = "new"
  webauthn_platform_first_factor = false
}

# __generated__ by Terraform from "signup-password::en"
resource "auth0_prompt_custom_text" "en_signup_password" {
  body     = jsonencode({})
  language = "en"
  prompt   = "signup-password"
}

# __generated__ by Terraform from "login::en"
resource "auth0_prompt_custom_text" "en_login" {
  body     = jsonencode({})
  language = "en"
  prompt   = "login"
}

# __generated__ by Terraform from "signup::en"
resource "auth0_prompt_custom_text" "en_signup" {
  body     = jsonencode({})
  language = "en"
  prompt   = "signup"
}

# __generated__ by Terraform from "69947cd1be21a17f3b9f7d5b"
resource "auth0_resource_server" "auth0_management_api" {
  allow_offline_access                            = false
  consent_policy                                  = jsonencode(null)
  identifier                                      = "https://k8sstormcenter.eu.auth0.com/api/v2/"
  name                                            = "Auth0 Management API"
  signing_alg                                     = "RS256"
  skip_consent_for_verifiable_first_party_clients = false
  token_lifetime                                  = 86400
  token_lifetime_for_web                          = 7200
  verification_location                           = null
  authorization_details {
    disable = true
    type    = null
  }
  proof_of_possession {
    disable  = true
    required = false
  }
  subject_type_authorization {
    client {
      policy = "require_client_grant"
    }
    user {
      policy = "allow_all"
    }
  }
  token_encryption {
    disable = true
  }
}

# __generated__ by Terraform from "phone-identifier-enrollment::en"
resource "auth0_prompt_custom_text" "en_phone_identifier_enrollment" {
  body     = jsonencode({})
  language = "en"
  prompt   = "phone-identifier-enrollment"
}

# __generated__ by Terraform from "mfa-sms::en"
resource "auth0_prompt_custom_text" "en_mfa_sms" {
  body     = jsonencode({})
  language = "en"
  prompt   = "mfa-sms"
}

# __generated__ by Terraform from "email-otp-challenge::en"
resource "auth0_prompt_custom_text" "en_email_otp_challenge" {
  body     = jsonencode({})
  language = "en"
  prompt   = "email-otp-challenge"
}

# __generated__ by Terraform from "login:login"
resource "auth0_prompt_screen_partial" "login_login" {
  prompt_type = "login"
  screen_name = "login"
}

# __generated__ by Terraform from "5akM0SumJG95tcRlP390pXzJoZkkYNDT"
resource "auth0_client_credentials" "sovereignsoc_pixie_cloud" {
  authentication_method = "client_secret_post"
  client_id             = "5akM0SumJG95tcRlP390pXzJoZkkYNDT"
  client_secret         = null # sensitive
}

# __generated__ by Terraform from "invitation::en"
resource "auth0_prompt_custom_text" "en_invitation" {
  body     = jsonencode({})
  language = "en"
  prompt   = "invitation"
}

# __generated__ by Terraform from "con_3HhKvUUeZr2ijQ90"
resource "auth0_connection_clients" "username_password_authentication" {
  connection_id   = "con_3HhKvUUeZr2ijQ90"
  enabled_clients = ["0DxLenl4KRr8g0fxmxsh7SjQfb7unbMe", "5akM0SumJG95tcRlP390pXzJoZkkYNDT"]
}

# __generated__ by Terraform from "mfa-otp::en"
resource "auth0_prompt_custom_text" "en_mfa_otp" {
  body     = jsonencode({})
  language = "en"
  prompt   = "mfa-otp"
}

# __generated__ by Terraform from "status::en"
resource "auth0_prompt_custom_text" "en_status" {
  body     = jsonencode({})
  language = "en"
  prompt   = "status"
}

# __generated__ by Terraform from "mfa-recovery-code::en"
resource "auth0_prompt_custom_text" "en_mfa_recovery_code" {
  body     = jsonencode({})
  language = "en"
  prompt   = "mfa-recovery-code"
}

# __generated__ by Terraform from "reset-password::en"
resource "auth0_prompt_custom_text" "en_reset_password" {
  body     = jsonencode({})
  language = "en"
  prompt   = "reset-password"
}

# __generated__ by Terraform from "signup:signup"
resource "auth0_prompt_screen_partial" "signup_signup" {
  prompt_type = "signup"
  screen_name = "signup"
}

# __generated__ by Terraform from "login-id:login-id"
resource "auth0_prompt_screen_partial" "login_id_login_id" {
  prompt_type = "login-id"
  screen_name = "login-id"
}

# __generated__ by Terraform from "69947cd1be21a17f3b9f7d5b"
resource "auth0_resource_server_scopes" "auth0_management_api" {
  resource_server_identifier = "https://k8sstormcenter.eu.auth0.com/api/v2/"
  scopes {
    description = "Blacklist Tokens"
    name        = "blacklist:tokens"
  }
  scopes {
    description = "Configure new custom domains"
    name        = "create:custom_domains"
  }
  scopes {
    description = "Create Actions"
    name        = "create:actions"
  }
  scopes {
    description = "Create Authentication Methods"
    name        = "create:authentication_methods"
  }
  scopes {
    description = "Create Client Credentials"
    name        = "create:client_credentials"
  }
  scopes {
    description = "Create Client Grants"
    name        = "create:client_grants"
  }
  scopes {
    description = "Create Client Keys"
    name        = "create:client_keys"
  }
  scopes {
    description = "Create Clients"
    name        = "create:clients"
  }
  scopes {
    description = "Create Connection Profiles"
    name        = "create:connection_profiles"
  }
  scopes {
    description = "Create Connections"
    name        = "create:connections"
  }
  scopes {
    description = "Create Custom User Blocks"
    name        = "create:user_custom_blocks"
  }
  scopes {
    description = "Create Customer Provided Public Signing Keys"
    name        = "create:custom_signing_keys"
  }
  scopes {
    description = "Create Device Credentials"
    name        = "create:device_credentials"
  }
  scopes {
    description = "Create Email Provider"
    name        = "create:email_provider"
  }
  scopes {
    description = "Create Flows Vault connections"
    name        = "create:flows_vault_connections"
  }
  scopes {
    description = "Create Flows"
    name        = "create:flows"
  }
  scopes {
    description = "Create Forms"
    name        = "create:forms"
  }
  scopes {
    description = "Create Group Roles"
    name        = "create:group_roles"
  }
  scopes {
    description = "Create Hooks"
    name        = "create:hooks"
  }
  scopes {
    description = "Create Network ACLs"
    name        = "create:network_acls"
  }
  scopes {
    description = "Create Organization Client Grants"
    name        = "create:organization_client_grants"
  }
  scopes {
    description = "Create Organization Discovery Domains"
    name        = "create:organization_discovery_domains"
  }
  scopes {
    description = "Create Organization Group Roles"
    name        = "create:organization_group_roles"
  }
  scopes {
    description = "Create Organization Groups"
    name        = "create:organization_groups"
  }
  scopes {
    description = "Create Organizations"
    name        = "create:organizations"
  }
  scopes {
    description = "Create Resource Servers"
    name        = "create:resource_servers"
  }
  scopes {
    description = "Create Rules"
    name        = "create:rules"
  }
  scopes {
    description = "Create SCIM configuration"
    name        = "create:scim_config"
  }
  scopes {
    description = "Create SCIM token"
    name        = "create:scim_token"
  }
  scopes {
    description = "Create SSO Access Tickets"
    name        = "create:sso_access_tickets"
  }
  scopes {
    description = "Create Self Service Profiles"
    name        = "create:self_service_profiles"
  }
  scopes {
    description = "Create Shields"
    name        = "create:shields"
  }
  scopes {
    description = "Create Token Exchange Profile"
    name        = "create:token_exchange_profiles"
  }
  scopes {
    description = "Create User Attribute Profiles"
    name        = "create:user_attribute_profiles"
  }
  scopes {
    description = "Create User Tickets"
    name        = "create:user_tickets"
  }
  scopes {
    description = "Create Users App Metadata"
    name        = "create:users_app_metadata"
  }
  scopes {
    description = "Create Users"
    name        = "create:users"
  }
  scopes {
    description = "Create Verifiable Digital Credential Templates"
    name        = "create:vdcs_templates"
  }
  scopes {
    description = "Create a Phone Notification Provider"
    name        = "create:phone_providers"
  }
  scopes {
    description = "Create a Phone Notification Template"
    name        = "create:phone_templates"
  }
  scopes {
    description = "Create connection keys"
    name        = "create:connections_keys"
  }
  scopes {
    description = "Create directory provisioning configurations"
    name        = "create:directory_provisionings"
  }
  scopes {
    description = "Create email templates"
    name        = "create:email_templates"
  }
  scopes {
    description = "Create encryption keys"
    name        = "create:encryption_keys"
  }
  scopes {
    description = "Create enrollment tickets for Guardian"
    name        = "create:guardian_enrollment_tickets"
  }
  scopes {
    description = "Create event streams"
    name        = "create:event_streams"
  }
  scopes {
    description = "Create log_streams"
    name        = "create:log_streams"
  }
  scopes {
    description = "Create organization connections"
    name        = "create:organization_connections"
  }
  scopes {
    description = "Create organization invitations"
    name        = "create:organization_invitations"
  }
  scopes {
    description = "Create organization member roles"
    name        = "create:organization_member_roles"
  }
  scopes {
    description = "Create organization members"
    name        = "create:organization_members"
  }
  scopes {
    description = "Create password checking jobs"
    name        = "create:passwords_checking_job"
  }
  scopes {
    description = "Create role members"
    name        = "create:role_members"
  }
  scopes {
    description = "Create roles"
    name        = "create:roles"
  }
  scopes {
    description = "Create signing keys"
    name        = "create:signing_keys"
  }
  scopes {
    description = "Delete Actions"
    name        = "delete:actions"
  }
  scopes {
    description = "Delete Anomaly Detection Blocks"
    name        = "delete:anomaly_blocks"
  }
  scopes {
    description = "Delete Authentication Methods"
    name        = "delete:authentication_methods"
  }
  scopes {
    description = "Delete Client Credentials"
    name        = "delete:client_credentials"
  }
  scopes {
    description = "Delete Client Grants"
    name        = "delete:client_grants"
  }
  scopes {
    description = "Delete Client Keys"
    name        = "delete:client_keys"
  }
  scopes {
    description = "Delete Clients"
    name        = "delete:clients"
  }
  scopes {
    description = "Delete Connection Profiles"
    name        = "delete:connection_profiles"
  }
  scopes {
    description = "Delete Connections"
    name        = "delete:connections"
  }
  scopes {
    description = "Delete Custom User Blocks"
    name        = "delete:user_custom_blocks"
  }
  scopes {
    description = "Delete Customer Provided Public Signing Keys"
    name        = "delete:custom_signing_keys"
  }
  scopes {
    description = "Delete Device Credentials"
    name        = "delete:device_credentials"
  }
  scopes {
    description = "Delete Email Provider"
    name        = "delete:email_provider"
  }
  scopes {
    description = "Delete Federated Connections Tokensets belonging to a user"
    name        = "delete:federated_connections_tokens"
  }
  scopes {
    description = "Delete Flows Executions"
    name        = "delete:flows_executions"
  }
  scopes {
    description = "Delete Flows Vault connections"
    name        = "delete:flows_vault_connections"
  }
  scopes {
    description = "Delete Flows"
    name        = "delete:flows"
  }
  scopes {
    description = "Delete Forms"
    name        = "delete:forms"
  }
  scopes {
    description = "Delete Group Roles"
    name        = "delete:group_roles"
  }
  scopes {
    description = "Delete Guardian enrollments"
    name        = "delete:guardian_enrollments"
  }
  scopes {
    description = "Delete Hooks"
    name        = "delete:hooks"
  }
  scopes {
    description = "Delete Network ACLs"
    name        = "delete:network_acls"
  }
  scopes {
    description = "Delete Organization Client Grants"
    name        = "delete:organization_client_grants"
  }
  scopes {
    description = "Delete Organization Discovery Domains"
    name        = "delete:organization_discovery_domains"
  }
  scopes {
    description = "Delete Organization Group Roles"
    name        = "delete:organization_group_roles"
  }
  scopes {
    description = "Delete Organization Groups"
    name        = "delete:organization_groups"
  }
  scopes {
    description = "Delete Organizations"
    name        = "delete:organizations"
  }
  scopes {
    description = "Delete Refresh Tokens"
    name        = "delete:refresh_tokens"
  }
  scopes {
    description = "Delete Resource Servers"
    name        = "delete:resource_servers"
  }
  scopes {
    description = "Delete Rules Configs"
    name        = "delete:rules_configs"
  }
  scopes {
    description = "Delete Rules"
    name        = "delete:rules"
  }
  scopes {
    description = "Delete SCIM configuration"
    name        = "delete:scim_config"
  }
  scopes {
    description = "Delete SCIM token"
    name        = "delete:scim_token"
  }
  scopes {
    description = "Delete SSO Access Tickets"
    name        = "delete:sso_access_tickets"
  }
  scopes {
    description = "Delete Self Service Profiles"
    name        = "delete:self_service_profiles"
  }
  scopes {
    description = "Delete Sessions"
    name        = "delete:sessions"
  }
  scopes {
    description = "Delete Shields"
    name        = "delete:shields"
  }
  scopes {
    description = "Delete Token Exchange Profile"
    name        = "delete:token_exchange_profiles"
  }
  scopes {
    description = "Delete User Attribute Profiles"
    name        = "delete:user_attribute_profiles"
  }
  scopes {
    description = "Delete User Grants"
    name        = "delete:grants"
  }
  scopes {
    description = "Delete Users App Metadata"
    name        = "delete:users_app_metadata"
  }
  scopes {
    description = "Delete Users"
    name        = "delete:users"
  }
  scopes {
    description = "Delete Verifiable Digital Credential Templates"
    name        = "delete:vdcs_templates"
  }
  scopes {
    description = "Delete a Phone Notification Provider"
    name        = "delete:phone_providers"
  }
  scopes {
    description = "Delete a Phone Notification Template"
    name        = "delete:phone_templates"
  }
  scopes {
    description = "Delete branding settings"
    name        = "delete:branding"
  }
  scopes {
    description = "Delete custom domains configurations"
    name        = "delete:custom_domains"
  }
  scopes {
    description = "Delete directory provisioning configurations"
    name        = "delete:directory_provisionings"
  }
  scopes {
    description = "Delete encryption keys"
    name        = "delete:encryption_keys"
  }
  scopes {
    description = "Delete event streams"
    name        = "delete:event_streams"
  }
  scopes {
    description = "Delete log_streams"
    name        = "delete:log_streams"
  }
  scopes {
    description = "Delete organization connections"
    name        = "delete:organization_connections"
  }
  scopes {
    description = "Delete organization invitations"
    name        = "delete:organization_invitations"
  }
  scopes {
    description = "Delete organization member roles"
    name        = "delete:organization_member_roles"
  }
  scopes {
    description = "Delete organization members"
    name        = "delete:organization_members"
  }
  scopes {
    description = "Delete roles"
    name        = "delete:roles"
  }
  scopes {
    description = "Deletes password checking job and all its resources"
    name        = "delete:passwords_checking_job"
  }
  scopes {
    description = "List Federated Connections Tokensets belonging to a user"
    name        = "read:federated_connections_tokens"
  }
  scopes {
    description = "Read Actions"
    name        = "read:actions"
  }
  scopes {
    description = "Read Anomaly Detection Blocks"
    name        = "read:anomaly_blocks"
  }
  scopes {
    description = "Read Authentication Methods"
    name        = "read:authentication_methods"
  }
  scopes {
    description = "Read Client Credentials"
    name        = "read:client_credentials"
  }
  scopes {
    description = "Read Client Grants"
    name        = "read:client_grants"
  }
  scopes {
    description = "Read Client Keys"
    name        = "read:client_keys"
  }
  scopes {
    description = "Read Clients"
    name        = "read:clients"
  }
  scopes {
    description = "Read Connection Profiles"
    name        = "read:connection_profiles"
  }
  scopes {
    description = "Read Connections Options"
    name        = "read:connections_options"
  }
  scopes {
    description = "Read Connections"
    name        = "read:connections"
  }
  scopes {
    description = "Read Custom User Blocks"
    name        = "read:user_custom_blocks"
  }
  scopes {
    description = "Read Customer Provided Public Signing Keys"
    name        = "read:custom_signing_keys"
  }
  scopes {
    description = "Read Device Credentials"
    name        = "read:device_credentials"
  }
  scopes {
    description = "Read Email Provider"
    name        = "read:email_provider"
  }
  scopes {
    description = "Read Flows Executions"
    name        = "read:flows_executions"
  }
  scopes {
    description = "Read Flows Vault connections"
    name        = "read:flows_vault_connections"
  }
  scopes {
    description = "Read Flows Vault items"
    name        = "read:flows_vault"
  }
  scopes {
    description = "Read Flows"
    name        = "read:flows"
  }
  scopes {
    description = "Read Forms"
    name        = "read:forms"
  }
  scopes {
    description = "Read Group Roles"
    name        = "read:group_roles"
  }
  scopes {
    description = "Read Guardian enrollments"
    name        = "read:guardian_enrollments"
  }
  scopes {
    description = "Read Guardian factors configuration"
    name        = "read:guardian_factors"
  }
  scopes {
    description = "Read Hooks"
    name        = "read:hooks"
  }
  scopes {
    description = "Read Insights"
    name        = "read:insights"
  }
  scopes {
    description = "Read Logs"
    name        = "read:logs"
  }
  scopes {
    description = "Read Multifactor Authentication policies"
    name        = "read:mfa_policies"
  }
  scopes {
    description = "Read Network ACLs"
    name        = "read:network_acls"
  }
  scopes {
    description = "Read Organization Client Grants"
    name        = "read:organization_client_grants"
  }
  scopes {
    description = "Read Organization Discovery Domains"
    name        = "read:organization_discovery_domains"
  }
  scopes {
    description = "Read Organization Group Roles"
    name        = "read:organization_group_roles"
  }
  scopes {
    description = "Read Organization Groups"
    name        = "read:organization_groups"
  }
  scopes {
    description = "Read Organization Member Effective Roles"
    name        = "read:organization_member_effective_roles"
  }
  scopes {
    description = "Read Organization Member Role Source Groups"
    name        = "read:organization_member_role_source_groups"
  }
  scopes {
    description = "Read Organizations"
    name        = "read:organizations"
  }
  scopes {
    description = "Read Refresh Tokens"
    name        = "read:refresh_tokens"
  }
  scopes {
    description = "Read Resource Servers"
    name        = "read:resource_servers"
  }
  scopes {
    description = "Read Rules Configs"
    name        = "read:rules_configs"
  }
  scopes {
    description = "Read Rules"
    name        = "read:rules"
  }
  scopes {
    description = "Read SCIM configuration"
    name        = "read:scim_config"
  }
  scopes {
    description = "Read SCIM token"
    name        = "read:scim_token"
  }
  scopes {
    description = "Read Self Service Profile Custom Texts"
    name        = "read:self_service_profile_custom_texts"
  }
  scopes {
    description = "Read Self Service Profiles"
    name        = "read:self_service_profiles"
  }
  scopes {
    description = "Read Sessions"
    name        = "read:sessions"
  }
  scopes {
    description = "Read Shields"
    name        = "read:shields"
  }
  scopes {
    description = "Read Stats"
    name        = "read:stats"
  }
  scopes {
    description = "Read Tenant Settings"
    name        = "read:tenant_settings"
  }
  scopes {
    description = "Read Token Exchange Profiles"
    name        = "read:token_exchange_profiles"
  }
  scopes {
    description = "Read Triggers"
    name        = "read:triggers"
  }
  scopes {
    description = "Read User Attribute Profiles"
    name        = "read:user_attribute_profiles"
  }
  scopes {
    description = "Read User Effective Permissions"
    name        = "read:user_effective_permissions"
  }
  scopes {
    description = "Read User Effective Roles"
    name        = "read:user_effective_roles"
  }
  scopes {
    description = "Read User Grants"
    name        = "read:grants"
  }
  scopes {
    description = "Read User Permission Source Roles"
    name        = "read:user_permission_source_roles"
  }
  scopes {
    description = "Read User Role Source Groups"
    name        = "read:user_role_source_groups"
  }
  scopes {
    description = "Read Users App Metadata"
    name        = "read:users_app_metadata"
  }
  scopes {
    description = "Read Users IDP tokens"
    name        = "read:user_idp_tokens"
  }
  scopes {
    description = "Read Users"
    name        = "read:users"
  }
  scopes {
    description = "Read Verifiable Digital Credential Templates"
    name        = "read:vdcs_templates"
  }
  scopes {
    description = "Read a Phone Notification Provider"
    name        = "read:phone_providers"
  }
  scopes {
    description = "Read a Phone Notification Template"
    name        = "read:phone_templates"
  }
  scopes {
    description = "Read attack protection"
    name        = "read:attack_protection"
  }
  scopes {
    description = "Read branding settings"
    name        = "read:branding"
  }
  scopes {
    description = "Read connection keys"
    name        = "read:connections_keys"
  }
  scopes {
    description = "Read custom domains configurations"
    name        = "read:custom_domains"
  }
  scopes {
    description = "Read directory provisioning configurations"
    name        = "read:directory_provisionings"
  }
  scopes {
    description = "Read email templates"
    name        = "read:email_templates"
  }
  scopes {
    description = "Read encryption keys"
    name        = "read:encryption_keys"
  }
  scopes {
    description = "Read entitlements"
    name        = "read:entitlements"
  }
  scopes {
    description = "Read entity limits"
    name        = "read:limits"
  }
  scopes {
    description = "Read event stream deliveries"
    name        = "read:event_deliveries"
  }
  scopes {
    description = "Read event streams"
    name        = "read:event_streams"
  }
  scopes {
    description = "Read log_streams"
    name        = "read:log_streams"
  }
  scopes {
    description = "Read logs relating to users"
    name        = "read:logs_users"
  }
  scopes {
    description = "Read organization connections"
    name        = "read:organization_connections"
  }
  scopes {
    description = "Read organization invitations"
    name        = "read:organization_invitations"
  }
  scopes {
    description = "Read organization member roles"
    name        = "read:organization_member_roles"
  }
  scopes {
    description = "Read organization members"
    name        = "read:organization_members"
  }
  scopes {
    description = "Read organization summary"
    name        = "read:organizations_summary"
  }
  scopes {
    description = "Read prompts settings"
    name        = "read:prompts"
  }
  scopes {
    description = "Read role members"
    name        = "read:role_members"
  }
  scopes {
    description = "Read roles"
    name        = "read:roles"
  }
  scopes {
    description = "Read signing keys"
    name        = "read:signing_keys"
  }
  scopes {
    description = "Redeliver event(s) to an event stream"
    name        = "update:event_deliveries"
  }
  scopes {
    description = "Update Actions"
    name        = "update:actions"
  }
  scopes {
    description = "Update Authentication Methods"
    name        = "update:authentication_methods"
  }
  scopes {
    description = "Update Client Credentials"
    name        = "update:client_credentials"
  }
  scopes {
    description = "Update Client Grants"
    name        = "update:client_grants"
  }
  scopes {
    description = "Update Client Keys"
    name        = "update:client_keys"
  }
  scopes {
    description = "Update Clients"
    name        = "update:clients"
  }
  scopes {
    description = "Update Connection Profiles"
    name        = "update:connection_profiles"
  }
  scopes {
    description = "Update Connections Options"
    name        = "update:connections_options"
  }
  scopes {
    description = "Update Connections"
    name        = "update:connections"
  }
  scopes {
    description = "Update Customer Provided Public Signing Keys"
    name        = "update:custom_signing_keys"
  }
  scopes {
    description = "Update Device Credentials"
    name        = "update:device_credentials"
  }
  scopes {
    description = "Update Email Provider"
    name        = "update:email_provider"
  }
  scopes {
    description = "Update Flows Vault connections"
    name        = "update:flows_vault_connections"
  }
  scopes {
    description = "Update Flows"
    name        = "update:flows"
  }
  scopes {
    description = "Update Forms"
    name        = "update:forms"
  }
  scopes {
    description = "Update Guardian factors"
    name        = "update:guardian_factors"
  }
  scopes {
    description = "Update Hooks"
    name        = "update:hooks"
  }
  scopes {
    description = "Update Multifactor Authentication policies"
    name        = "update:mfa_policies"
  }
  scopes {
    description = "Update Network ACLs"
    name        = "update:network_acls"
  }
  scopes {
    description = "Update Organization Discovery Domains"
    name        = "update:organization_discovery_domains"
  }
  scopes {
    description = "Update Organizations"
    name        = "update:organizations"
  }
  scopes {
    description = "Update Refresh Tokens"
    name        = "update:refresh_tokens"
  }
  scopes {
    description = "Update Resource Servers"
    name        = "update:resource_servers"
  }
  scopes {
    description = "Update Rules Configs"
    name        = "update:rules_configs"
  }
  scopes {
    description = "Update Rules"
    name        = "update:rules"
  }
  scopes {
    description = "Update SCIM configuration"
    name        = "update:scim_config"
  }
  scopes {
    description = "Update Self Service Profile Custom Texts"
    name        = "update:self_service_profile_custom_texts"
  }
  scopes {
    description = "Update Self Service Profiles"
    name        = "update:self_service_profiles"
  }
  scopes {
    description = "Update Sessions"
    name        = "update:sessions"
  }
  scopes {
    description = "Update Shields"
    name        = "update:shields"
  }
  scopes {
    description = "Update Tenant Settings"
    name        = "update:tenant_settings"
  }
  scopes {
    description = "Update Token Exchange Profile"
    name        = "update:token_exchange_profiles"
  }
  scopes {
    description = "Update Triggers"
    name        = "update:triggers"
  }
  scopes {
    description = "Update User Attribute Profiles"
    name        = "update:user_attribute_profiles"
  }
  scopes {
    description = "Update Users App Metadata"
    name        = "update:users_app_metadata"
  }
  scopes {
    description = "Update Users"
    name        = "update:users"
  }
  scopes {
    description = "Update Verifiable Digital Credential Templates"
    name        = "update:vdcs_templates"
  }
  scopes {
    description = "Update a Phone Notification Provider"
    name        = "update:phone_providers"
  }
  scopes {
    description = "Update a Phone Notification Template"
    name        = "update:phone_templates"
  }
  scopes {
    description = "Update attack protection"
    name        = "update:attack_protection"
  }
  scopes {
    description = "Update branding settings"
    name        = "update:branding"
  }
  scopes {
    description = "Update connection keys"
    name        = "update:connections_keys"
  }
  scopes {
    description = "Update custom domain configurations"
    name        = "update:custom_domains"
  }
  scopes {
    description = "Update directory provisioning configurations"
    name        = "update:directory_provisionings"
  }
  scopes {
    description = "Update email templates"
    name        = "update:email_templates"
  }
  scopes {
    description = "Update encryption keys"
    name        = "update:encryption_keys"
  }
  scopes {
    description = "Update entity limits"
    name        = "update:limits"
  }
  scopes {
    description = "Update event streams"
    name        = "update:event_streams"
  }
  scopes {
    description = "Update log_streams"
    name        = "update:log_streams"
  }
  scopes {
    description = "Update organization connections"
    name        = "update:organization_connections"
  }
  scopes {
    description = "Update prompts settings"
    name        = "update:prompts"
  }
  scopes {
    description = "Update role members"
    name        = "delete:role_members"
  }
  scopes {
    description = "Update roles"
    name        = "update:roles"
  }
  scopes {
    description = "Update signing keys"
    name        = "update:signing_keys"
  }
}

# __generated__ by Terraform from "logout::en"
resource "auth0_prompt_custom_text" "en_logout" {
  body     = jsonencode({})
  language = "en"
  prompt   = "logout"
}

# __generated__ by Terraform from "signup-id::en"
resource "auth0_prompt_custom_text" "en_signup_id" {
  body     = jsonencode({})
  language = "en"
  prompt   = "signup-id"
}

# __generated__ by Terraform from "mfa-voice::en"
resource "auth0_prompt_custom_text" "en_mfa_voice" {
  body     = jsonencode({})
  language = "en"
  prompt   = "mfa-voice"
}

# __generated__ by Terraform from "custom-form::en"
resource "auth0_prompt_custom_text" "en_custom_form" {
  body     = jsonencode({})
  language = "en"
  prompt   = "custom-form"
}

# __generated__ by Terraform from "email-verification::en"
resource "auth0_prompt_custom_text" "en_email_verification" {
  body     = jsonencode({})
  language = "en"
  prompt   = "email-verification"
}

# __generated__ by Terraform from "b669af3f-2d77-4205-92f3-76140f856208"
resource "auth0_branding" "branding" {
}

# __generated__ by Terraform from "5akM0SumJG95tcRlP390pXzJoZkkYNDT"
resource "auth0_client" "sovereignsoc_pixie_cloud" {
  allowed_clients                                      = []
  allowed_logout_urls                                  = []
  allowed_origins                                      = []
  app_type                                             = "non_interactive"
  callbacks                                            = ["https://${var.cloud_domain}/auth/callback", "https://work.${var.cloud_domain}/auth/callback"]
  client_aliases                                       = []
  client_metadata                                      = {}
  compliance_level                                     = null
  cross_origin_auth                                    = false
  cross_origin_loc                                     = null
  custom_login_page                                    = null
  custom_login_page_on                                 = true
  description                                          = null
  encryption_key                                       = null
  form_template                                        = null
  grant_types                                          = ["client_credentials", "implicit", "authorization_code", "refresh_token"]
  initiate_login_uri                                   = null
  is_first_party                                       = true
  is_token_endpoint_ip_header_trusted                  = false
  logo_uri                                             = null
  name                                                 = "SovereignSoc - Pixie Cloud"
  oidc_conformant                                      = true
  require_proof_of_possession                          = false
  require_pushed_authorization_requests                = false
  resource_server_identifier                           = null
  skip_non_verifiable_callback_uri_confirmation_prompt = jsonencode(null)
  sso                                                  = false
  sso_disabled                                         = false
  web_origins                                          = []
  default_organization {
    disable = true
    flows   = []
  }
  jwt_configuration {
    alg                 = "RS256"
    lifetime_in_seconds = 36000
    scopes              = {}
    secret_encoded      = false
  }
  native_social_login {
    apple {
      enabled = false
    }
    facebook {
      enabled = false
    }
    google {
      enabled = false
    }
  }
  refresh_token {
    expiration_type              = "non-expiring"
    idle_token_lifetime          = 2592000
    infinite_idle_token_lifetime = true
    infinite_token_lifetime      = true
    leeway                       = 0
    rotation_type                = "non-rotating"
    token_lifetime               = 31557600
  }
}

# __generated__ by Terraform from "e5ebee0e-0ac3-4f58-bd5b-c375377c9b24"
resource "auth0_attack_protection" "attack_protection" {
  bot_detection {
    allowlist                       = []
    bot_detection_level             = "medium"
    challenge_password_policy       = "never"
    challenge_password_reset_policy = "never"
    challenge_passwordless_policy   = "never"
    monitoring_mode_enabled         = false
  }
  breached_password_detection {
    admin_notification_frequency = []
    enabled                      = false
    method                       = "standard"
    shields                      = []
    pre_change_password {
      shields = []
    }
    pre_user_registration {
      shields = []
    }
  }
  brute_force_protection {
    allowlist    = []
    enabled      = true
    max_attempts = 10
    mode         = "count_per_identifier_and_ip"
    shields      = ["block", "user_notification"]
  }
  captcha {
    active_provider_id = "auth_challenge"
    arkose {
      client_subdomain = "client-api"
      fail_open        = false
      secret           = null # sensitive
      site_key         = ""
      verify_subdomain = "verify-api"
    }
    auth_challenge {
      fail_open = false
    }
    friendly_captcha {
      secret   = null # sensitive
      site_key = ""
    }
    hcaptcha {
      secret   = null # sensitive
      site_key = ""
    }
    recaptcha_enterprise {
      api_key    = null # sensitive
      project_id = ""
      site_key   = ""
    }
    recaptcha_v2 {
      secret   = null # sensitive
      site_key = ""
    }
  }
  suspicious_ip_throttling {
    allowlist = []
    enabled   = true
    shields   = ["admin_notification", "block"]
    pre_login {
      max_attempts = 100
      rate         = 864000
    }
    pre_user_registration {
      max_attempts = 50
      rate         = 1200
    }
  }
}
