#! /usr/bin/env python3

import gitlab
import requests
from bs4 import BeautifulSoup
import os

# GitlabOperator is a controller class for managing GitLab resources.
# It supports preparing test data according to configuration, including:
# - Users
# - Groups
# - Projects
# - Importing specified projects
# It also provides methods to verify the completeness of the data,
# ensuring the expected number of users, groups, and projects exist.
class GitlabOperator:
    def __init__(self, url, username, password):
        """
        Initialize the GitlabOperator with GitLab server information and authenticate using username and password.
        Also ensure that project import is enabled on the GitLab instance.

        :param url: The URL of the GitLab instance.
        :param username: The username for authentication.
        :param password: The password for authentication.
        """
        self.token = self._get_token(url, username, password)
        if self.token is None:
            raise Exception("Failed to get GitLab token")
        self.gl = gitlab.Gitlab(url, private_token=self.token)
        self.gl.auth()
        self._ensure_import_enabled()   

    def get_authenticity_token(self, url):
        """
        Get the authenticity token (CSRF token) from GitLab login page.
        
        :param url: GitLab instance URL
        :return: Authenticity token string or None if not found
        """
        try:
            # Get login page
            response = requests.get(f"{url}/users/sign_in")
            response.raise_for_status()
            
            # Parse HTML to get token
            soup = BeautifulSoup(response.text, 'html.parser')
            
            # Method 1: Look for input field with name 'authenticity_token'
            token_input = soup.find('input', {'name': 'authenticity_token'})
            if token_input and token_input.get('value'):
                return token_input['value']
            
            # Method 2: Look for meta tag with name 'csrf-token'
            meta_token = soup.find('meta', {'name': 'csrf-token'})
            if meta_token and meta_token.get('content'):
                return meta_token['content']
            
            # Method 3: Look for meta tag with name 'csrf-param' and find corresponding input
            meta_param = soup.find('meta', {'name': 'csrf-param'})
            if meta_param:
                param_name = meta_param.get('content', 'authenticity_token')
                token_input = soup.find('input', {'name': param_name})
                if token_input and token_input.get('value'):
                    return token_input.get('value')
            
            # Method 4: Look for any input with 'token' in the name
            for input_tag in soup.find_all('input'):
                if 'token' in input_tag.get('name', '').lower() and input_tag.get('value'):
                    return input_tag.get('value')
            
            return None
            
        except Exception as e:
            print(f"Error getting authenticity token: {e}")
            return None

    def create_gitlab_token(self, url, username, password):
        """
        Create a GitLab personal access token using username and password authentication.
        
        :param url: GitLab instance URL
        :param username: Username for authentication
        :param password: Password for authentication
        :return: Personal access token string or None if failed
        """
        session = requests.Session()
        
        # Set proper headers to mimic a real browser
        session.headers.update({
            'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
            'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8',
            'Accept-Language': 'en-US,en;q=0.9',
            'Accept-Encoding': 'gzip, deflate, br',
            'DNT': '1',
            'Connection': 'keep-alive',
            'Upgrade-Insecure-Requests': '1'
        })
        
        try:
            # Step 1: Get the login page and extract CSRF token
            print(f"🔍 Getting login page from {url}/users/sign_in")
            response = session.get(f"{url}/users/sign_in")
            response.raise_for_status()
            
            soup = BeautifulSoup(response.text, 'html.parser')
            
            # Try multiple ways to get the authenticity token
            authenticity_token = None
            
            # Method 1: Look for input field
            token_input = soup.find('input', {'name': 'authenticity_token'})
            if token_input and token_input.get('value'):
                authenticity_token = token_input['value']
            
            # Method 2: Look for meta tag
            if not authenticity_token:
                meta_token = soup.find('meta', {'name': 'csrf-token'})
                if meta_token:
                    authenticity_token = meta_token.get('content')
            
            # Method 3: Look for meta tag with different name
            if not authenticity_token:
                meta_token = soup.find('meta', {'name': 'csrf-param'})
                if meta_token:
                    param_name = meta_token.get('content', 'authenticity_token')
                    token_input = soup.find('input', {'name': param_name})
                    if token_input:
                        authenticity_token = token_input.get('value')
            
            if not authenticity_token:
                raise Exception("Could not find authenticity token in login page")
            
            print(f"✅ Found authenticity token: {authenticity_token[:20]}...")
            
            # Step 2: Perform login
            print(f"🔐 Attempting login for user: {username}")
            login_data = {
                'user[login]': username,
                'user[password]': password,
                'user[remember_me]': '0',
                'authenticity_token': authenticity_token
            }
            
            # Try different login endpoints
            login_endpoints = [
                f"{url}/users/sign_in",
            ]
            
            login_success = False
            for endpoint in login_endpoints:
                try:
                    response = session.post(
                        endpoint,
                        data=login_data,
                        allow_redirects=True,
                        timeout=30
                    )
                    
                    # Check if login was successful
                    if response.status_code in [200, 302]:
                        # Verify login success by checking if we can access user profile
                        profile_response = session.get(f"{url}/-/user_settings/profile")
                        if profile_response.status_code == 200 and username.lower() in profile_response.text.lower():
                            login_success = True
                            print(f"✅ Login successful via {endpoint}")
                            break
                        else:
                            print(f"⚠️  Login attempt via {endpoint} returned {response.status_code}, but profile check failed")
                    else:
                        print(f"⚠️  Login attempt via {endpoint} returned {response.status_code}")
                        
                except Exception as e:
                    print(f"⚠️  Login attempt via {endpoint} failed: {e}")
                    continue
            
            if not login_success:
                raise Exception("All login attempts failed")
            
            # Step 3: Create personal access token via web interface
            print("🔑 Creating personal access token via web interface...")
            
            # Get the personal access tokens page
            tokens_page_url = f"{url}/-/user_settings/personal_access_tokens"
            response = session.get(tokens_page_url)
            if response.status_code != 200:
                raise Exception(f"Failed to access tokens page: {response.status_code}")
            
            # Parse the page to get the authenticity token
            soup = BeautifulSoup(response.text, 'html.parser')
            
            # Find the authenticity token for the token creation form
            token_form = soup.find('form', {'action': lambda x: x and 'personal_access_tokens' in x})
            if not token_form:
                # Try alternative form selectors
                token_form = soup.find('form', {'id': 'new_personal_access_token'})
            
            if not token_form:
                raise Exception("Could not find personal access token creation form")
            
            # Get the authenticity token from the form
            form_token_input = token_form.find('input', {'name': 'authenticity_token'})
            if not form_token_input:
                # Try to get from meta tag
                meta_token = soup.find('meta', {'name': 'csrf-token'})
                if meta_token:
                    form_authenticity_token = meta_token.get('content')
                else:
                    raise Exception("Could not find authenticity token for token creation form")
            else:
                form_authenticity_token = form_token_input.get('value')
            
            if not form_authenticity_token:
                raise Exception("Authenticity token is empty")
            
            print(f"✅ Found form authenticity token: {form_authenticity_token[:20]}...")
            
            # Prepare token creation data
            token_creation_data = {
                'authenticity_token': form_authenticity_token,
                'personal_access_token[name]': 'full-access-token',
                'personal_access_token[expires_at]': '',  # No expiration
                'personal_access_token[scopes][]': [
                    'api', 'read_api', 'read_user', 'create_runner', 'manage_runner', 'k8s_proxy', 'read_repository', 'write_repository', 'ai_features', 'sudo', 'admin_mode', 'read_service_ping'
                ]
            }
            
            # Submit the token creation form
            token_creation_url = f"{url}/-/user_settings/personal_access_tokens"
            response = session.post(
                token_creation_url,
                data=token_creation_data,
                allow_redirects=True,
                timeout=30
            )
            
            # Check if token creation was successful
            if response.status_code in [200, 302]:
                # Try to extract the token from the response
                # GitLab usually shows the token on the next page after creation
                soup = BeautifulSoup(response.text, 'html.parser')
                
                # Look for the token in various possible locations
                token = None
                
                # Method 1: Look for token in success message or token display area
                token_elements = soup.find_all(['code', 'pre', 'input'], string=lambda text: text and text.startswith('glpat-'))
                if token_elements:
                    token = token_elements[0].get_text().strip()
                
                # Method 2: Look for token in page content
                if not token:
                    import re
                    token_pattern = r'glpat-[a-zA-Z0-9_-]{20,}'
                    matches = re.findall(token_pattern, response.text)
                    if matches:
                        token = matches[0]
                
                # Method 3: Check if we need to go to the tokens list page
                if not token:
                    # Try to access the tokens list page to see if token was created
                    tokens_list_url = f"{url}/-/user_settings/personal_access_tokens"
                    list_response = session.get(tokens_list_url)
                    if list_response.status_code == 200:
                        list_soup = BeautifulSoup(list_response.text, 'html.parser')
                        # Look for the newly created token in the list
                        token_elements = list_soup.find_all(['code', 'pre', 'input'], string=lambda text: text and text.startswith('glpat-'))
                        if token_elements:
                            token = token_elements[0].get_text().strip()
                print(f"✅ Personal access token created successfully via web interface")
                return token

            else:
                error_msg = f"Failed to create token via web interface: {response.status_code}"
                try:
                    error_detail = response.text[:500]
                    error_msg += f" - {error_detail}"
                except:
                    pass
                raise Exception(error_msg)
                
        except requests.exceptions.RequestException as e:
            raise Exception(f"Network error during token creation: {e}")
        except Exception as e:
            raise Exception(f"Token creation failed: {e}")

    def _ensure_import_enabled(self):
        """
        Ensure that the GitLab instance is configured to allow project imports.
        This method checks the import sources and enables them if necessary.
        """
        try:
            settings = self.gl.settings.get()
            # Handle settings as dictionary
            need_update = False
            import_sources = settings.import_sources
            if 'manifest' not in import_sources:
                need_update = True
                import_sources.append('manifest')
            if 'gitlab_project' not in import_sources:
                need_update = True
                import_sources.append('gitlab_project')
            if need_update:
                self.gl.settings.update(new_data={'import_sources': import_sources})
                print("✅ GitLab project import enabled")
        except Exception as e:
            print(f"Failed to ensure import is enabled: {e}")
            raise e

    def authenticate_with_token(self, token):
        """
        Re-authenticate using a personal access token.

        :param token: The personal access token to use for authentication.
        """
        try:
            self.gl = gitlab.Gitlab(self.gl.url, private_token=token)
            self.gl.auth()
            print("Successfully authenticated with token")
        except Exception as e:
            print(f"Failed to authenticate with token: {e}")

    def create_user(self, user_config):
        """
        Create a user if it doesn't already exist.

        :param user_config: Dict with user attributes.
        :return: User object if created or found, None if failed.
        """
        try:
            # Check if user already exists by username or email
            username = user_config.get('username')
            email = user_config.get('email')
            
            existing_user = self.find_user_by_username_or_email(username, email)
            if existing_user:
                print(f"⚠️  User '{username or email}' already exists (ID: {existing_user.id}). Skipping creation.")
                return existing_user
            
            # Create new user
            print(f"👤 Creating new user: {username or email}")
            user = self.gl.users.create(user_config)
            print(f"✅ User '{user.username}' created successfully (ID: {user.id})")
            return user
            
        except gitlab.exceptions.GitlabCreateError as e:
            print(f"❌ User creation failed: {e}")
            return None
        except Exception as e:
            print(f"❌ Unexpected error during user creation: {e}")
            return None

    def create_users(self, user_configs):
        """
        Create multiple users, skipping those that already exist.

        :param user_configs: List of dicts with user attributes.
        :return: List of created or found user objects.
        """
        created_users = []
        
        for user_config in user_configs:
            user = self.create_user(user_config)
            if user:
                created_users.append(user)
        
        print(f"📊 User creation summary: {len(created_users)}/{len(user_configs)} users processed")
        return created_users

    def find_user_by_username_or_email(self, username=None, email=None):
        """
        Find a user by username or email.
        
        :param username: Username to search for
        :param email: Email to search for
        :return: User object if found, None otherwise
        """
        try:
            # Search by username
            if username:
                users = self.gl.users.list(search=username, all=True)
                for user in users:
                    if user.username == username:
                        return user
            
            # Search by email
            if email:
                users = self.gl.users.list(search=email, all=True)
                for user in users:
                    if user.email == email:
                        return user
            
            return None
            
        except Exception as e:
            print(f"Error finding user: {e}")
            return None

    def create_group(self, group_config):
        """
        Create a group if it doesn't already exist.

        :param group_config: Dict with group attributes.
        :return: Group object if created or found, None if failed.
        """
        try:
            # Check if group already exists by name or path
            name = group_config.get('name')
            path = group_config.get('path')
            
            existing_group = self.find_group_by_name_or_path(name, path)
            if existing_group:
                print(f"⚠️  Group '{name or path}' already exists (ID: {existing_group.id}). Skipping creation.")
                return existing_group
            
            # Create new group
            print(f"👥 Creating new group: {name or path}")
            group = self.gl.groups.create(group_config)
            print(f"✅ Group '{group.name}' created successfully (ID: {group.id})")
            return group
            
        except Exception as e:
            print(f"❌ Unexpected error during group creation: {e}")
            raise e
        
    def delete_group(self, group_config):
        """
        Create a group if it doesn't already exist.

        :param group_config: Dict with group attributes.
        :return: Group object if created or found, None if failed.
        """
        try:
            # Check if group already exists by name or path
            name = group_config.get('name')
            path = group_config.get('path')
            
            group = self.find_group_by_name_or_path(name, path)
            if group is None:
                print(f"⚠️  Group '{name or path}' not found. Skipping deletion.")
                return None
            
            # Delete group
            print(f"👥 Deleting group: {name or path}")
            self.gl.groups.delete(group.id)
            print(f"✅ Group '{group.name}' deleted successfully (ID: {group.id})")
            return True
            
        except Exception as e:
            print(f"❌ Unexpected error during group creation: {e}")
            raise None

    def find_group_by_name_or_path(self, name=None, path=None, parent_id=None):
        """
        Find a group by name or path.
        
        :param name: Group name to search for
        :param path: Group path to search for
        :return: Group object if found, None otherwise
        """
        try:
            # Search by name
            if name:
                groups = self.gl.groups.list(search=name, parent_id=parent_id, all=True)
                for group in groups:
                    if group.name == name:
                        return group
            
            # Search by path
            if path:
                groups = self.gl.groups.list(search=path, all=True)
                for group in groups:
                    if group.path == path:
                        return group
            
            return None
            
        except Exception as e:
            print(f"Error finding group: {e}")
            return None

    def create_project(self, project_config):
        """
        Create a project if it doesn't already exist.

        :param project_config: Dict with project attributes.
        :return: Project object if created or found, None if failed.
        """
        try:
            # Check if project already exists by name
            name = project_config.get('name')
            namespace_id = project_config.get('namespace_id')
            
            existing_project = self.find_project_by_name(name, namespace_id)
            if existing_project:
                print(f"⚠️  Project '{name}' already exists (ID: {existing_project.id}). Skipping creation.")
                return existing_project
            
            # Create new project
            print(f"📁 Creating new project: {name}")
            project = self.gl.projects.create(project_config)
            print(f"✅ Project '{project.name}' created successfully (ID: {project.id})")
            return project
            
        except Exception as e:
            print(f"❌ Unexpected error during project creation: {e}")
            raise e

    def create_project_from_url(self, namespace_id, project_name, import_url=None):
        """
        Create a project from URL or create empty project.

        :param namespace_id: The target namespace ID.
        :param project_name: The name for the project.
        :param import_url: The URL of the project to import (optional).
        :return: Project object if created or found, None if failed.
        """
        try:
            project_config = {
                'namespace_id': namespace_id,
                'name': project_name
            }
            
            if import_url:
                project_config['import_url'] = import_url
            
            return self.create_project(project_config)
            
        except Exception as e:
            print(f"❌ Project creation from URL failed: {e}")
            return e

    def import_project_from_file(self, file_path, namespace_id, project_name, overwrite=False):
        """
        Import a project from a local file (tar.gz, zip, etc.).
        
        :param file_path: Path to the project archive file
        :param namespace_id: The target namespace ID
        :param project_name: The name for the imported project
        :param overwrite: Whether to overwrite existing project
        :return: Project object if successful, None otherwise
        """
        try:
            print(f"📁 Importing project from file: {file_path}")
            
            # Check if file exists
            if not os.path.exists(file_path):
                raise FileNotFoundError(f"Project file not found: {file_path}")
            
            # Check if project already exists
            existing_project = self.find_project_by_name(project_name, namespace_id)
            if existing_project and not overwrite:
                print(f"⚠️  Project '{project_name}' already exists. Use overwrite=True to overwrite.")
                return existing_project
            
            # Open file as BufferedReader and pass to import_project
            with open(file_path, 'rb') as file_obj:
                project = self.gl.projects.import_project(
                    file=file_obj,
                    path=project_name,
                    namespace=namespace_id,
                    overwrite=overwrite
                )
            
            print(f"✅ Project '{project_name}' imported successfully from file")
            is_imported = self.wait_for_import_completion(project["id"])
            if not is_imported:
                raise Exception(f"Project import failed")
            return project
            
        except Exception as e:
            print(f"❌ Failed to import project from file: {e}")
            raise e

    def find_project_by_name(self, project_name, namespace_id=None):
        """
        Find a project by name in the specified namespace.
        
        :param project_name: Name of the project to find
        :param namespace_id: Namespace ID to search in (optional)
        :return: Project object if found, None otherwise
        """
        try:
            # Search for projects by name
            projects = self.gl.projects.list(search=project_name, all=True)
            
            for project in projects:
                if project.name == project_name:
                    # If namespace_id is specified, check if it matches
                    if namespace_id is None or project.namespace['id'] == namespace_id:
                        return project
            
            return None
            
        except Exception as e:
            print(f"Error finding project: {e}")
            return None

    def get_import_status(self, project_id):
        """
        Get the import status of a project.
        
        :param project_id: ID of the project to check
        :return: Import status information
        """
        try:
            project = self.gl.http_get(f"/projects/{project_id}/import")
            return project
        except Exception as e:
            print(f"Error getting import status: {e}")
            raise e

    def wait_for_import_completion(self, project_id, timeout=300, check_interval=10):
        """
        Wait for project import to complete.
        
        :param project_id: ID of the project to monitor
        :param timeout: Maximum time to wait in seconds
        :param check_interval: Interval between status checks in seconds
        :return: True if import completed successfully, False otherwise
        """
        try:
            print(f"⏳ Waiting for project {project_id} import to complete...")
            
            import time
            start_time = time.time()
            
            while time.time() - start_time < timeout:
                status = self.get_import_status(project_id)
                if not status:
                    print("❌ Failed to get import status")
                    return False
                
                if status['import_status'] == 'finished':
                    print(f"✅ Project import completed successfully")
                    return True
                elif status['import_status'] == 'failed':
                    print(f"❌ Project import failed: {status.get('import_error', 'Unknown error')}")
                    return False
                elif status['import_status'] in ['started', 'scheduled']:
                    print(f"🔄 Import in progress... (status: {status['import_status']})")
                else:
                    print(f"⏸️  Import status: {status['import_status']}")
                
                time.sleep(check_interval)
            
            print(f"⏰ Import timeout after {timeout} seconds")
            return False
            
        except Exception as e:
            print(f"Error waiting for import completion: {e}")
            return False

    def _get_token(self, url, username, password):
        """
        Get GitLab token using multiple methods.
        
        :param url: GitLab instance URL
        :param username: Username for authentication
        :param password: Password for authentication
        :return: Personal access token string or None if failed
        """
        # Method 1: Try to create token using username/password
        try:
            print("🔐 Attempting to create token using username/password...")
            token = self.create_gitlab_token(url, username, password)
            if token:
                return token
        except Exception as e:
            print(f"⚠️  Username/password authentication failed: {e}")
            
        return None

    def get_branch_list(self, project):
        """
        Get the branch list of a project.
        
        :param project: Project object.
        :return: Branch list if successful, None if failed.
        """
        try:
            return self.gl.projects.get(project.id).branches.list()
        except Exception as e:
            print(f"Error getting branch list: {e}")
            raise e
    
    def get_issue_list(self, project):
        """
        Get the issue list of a project.
        
        :param project: Project object.
        :return: Issue list if successful, None if failed.
        """
        try:
            return self.gl.projects.get(project.id).issues.list()
        except Exception as e:
            print(f"Error getting issue list: {e}")
            raise e
    
    def get_merge_request_list(self, project):
        """
        Get the merge request list of a project.
        
        :param project: Project object.
        :return: Merge request list if successful, None if failed.
        """
        try:
            return self.gl.projects.get(project.id).mergerequests.list()
        except Exception as e:
            print(f"Error getting merge request list: {e}")
            raise e

    def get_commit_list(self, project):
        """
        Get the commit list of a project.
        
        :param project: Project object.
        :return: Commit list if successful, None if failed.
        """
        try:
            return self.gl.projects.get(project.id).commits.list()
        except Exception as e:
            print(f"Error getting commit list: {e}")
            raise e

    def get_issue_comment_list(self, project, issue_id):
        """
        Get the issue comment list of a project.
        
        :param project: Project object.
        :param issue_id: Issue ID.
        :return: Issue comment list if successful, None if failed.
        """
        try:
            return self.gl.projects.get(project.id).issues.get(issue_id).notes.list()
        except Exception as e:
            print(f"Error getting issue comment list: {e}")
            raise e

    def get_upload_files(self, project):
        """
        Get the upload file of a project.
        
        :param project: Project object.
        :return: Upload file if successful, None if failed.
        """
        try:
            upload_files = []
            uploads = self.gl.http_get(f"/projects/{project.id}/uploads")
            for upload in uploads:
                upload_file = self.gl.http_get(f"/projects/{project.id}/uploads/{upload['id']}")
                upload_files.append(upload_file)
            return upload_files
        except Exception as e:
            print(f"Error getting upload file: {e}")
            raise e
