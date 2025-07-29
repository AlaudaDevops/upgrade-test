#! /usr/bin/env python3

import os
import time
import yaml
import pytest
from kubernetes import client, config
from kubernetes.client.rest import ApiException
from gitlab_operator import GitlabOperator
from gitlab_manager import GitlabManager

class TestUpgrade:
    @classmethod
    def setup_class(cls):
        """
        Initialize the Kubernetes client for use in test cases.
        """
        # Load kubeconfig and initialize the client
        config.load_kube_config()
        cls.kube_client = client.ApiClient()
        cls.custom_api = client.CustomObjectsApi(cls.kube_client)
        cls.core_api = client.CoreV1Api(cls.kube_client)
        # Initialize GitLab template generator
        cls.gitlab_manager = GitlabManager()
        
        # Load test configuration
        cls.test_config = cls._load_test_config()
        cls.gitlab_operator = None
        
        # Initialize created data
        cls.gitlab_data = {
            "version_group": cls.test_config["version_group"],
            "groups": [],
            "projects": [],
            "import_project": cls.test_config["import_project"]
        }
        for i in range(1, cls.test_config["generation_rules"]["sub_groups_count"] + 1):
            cls.gitlab_data["groups"].append(f'group{i}')
        for i in range(1, cls.test_config["generation_rules"]["projects_per_sub_group"] + 1):
            cls.gitlab_data["projects"].append(f'project{i}')

    @classmethod
    def _init_gitlab_operator(cls):
        """
        Initialize the GitLab operator.
        """
        if cls.gitlab_operator is not None:
            return cls.gitlab_operator
        
        password = cls.test_config["gitlab"]["gitlab_root_password"]
        gitlab = cls.gitlab_manager.get_gitlab_instance(
            cls.test_config["gitlab"]["namespace"],
            cls.test_config["gitlab"]["name"]
        )
        if gitlab is None:
            pytest.fail("GitLab instance not found")
        cls.gitlab_operator = GitlabOperator(
            url=gitlab["spec"]["externalURL"],
            username="root",
            password=password,
        )
        return cls.gitlab_operator
    
    @classmethod
    def _load_test_config(cls):
        """
        Load test configuration from YAML file.
        
        :return: Dictionary containing test configuration
        """
        config_path = os.getenv("UPGRADE_CONFIG", "./config.yaml")
        if not os.path.exists(config_path):
            # Create default configuration if file doesn't exist
            default_config = {
                "gitlab": {
                    "namespace": "testing-gitlab-upgrade",
                    "name": "upgrade-test-gitlab"
                },
                "import_project": {
                    "file_path": "testdata/repo/test-upgrade-repo_export.tar.gz",
                    "project_name": "test-upgrade-repo",
                    "description": "Test repository for upgrade validation"
                },
                "version_group": {
                    "name": "v17.4.2",
                    "path": "v17.4.2",
                    "description": "Test data for GitLab version 17.4.2 upgrade testing"
                },
                "generation_rules": {
                    "sub_groups_count": 3,
                    "projects_per_sub_group": 3,
                    "sub_group_prefix": "group",
                    "project_prefix": "project"
                }
            }
            
            # Save default configuration
            with open(config_path, 'w') as f:
                yaml.dump(default_config, f, default_flow_style=False)
            
            return default_config
        
        # Load existing configuration
        with open(config_path, 'r') as f:
            config = yaml.safe_load(f)
        
        # Validate configuration
        cls._validate_test_config(config)
        return config

    @classmethod
    def _validate_test_config(self, config):
        """
        Validate the test configuration structure.
        
        :param config: Configuration dictionary to validate
        :raises: ValueError if configuration is invalid
        """
        required_sections = ["import_project", "version_group", "generation_rules"]
        for section in required_sections:
            if section not in config:
                raise ValueError(f"Missing required configuration section: {section}")
        
        # Validate import project
        import_project = config["import_project"]
        if not isinstance(import_project, dict):
            raise ValueError("import_project must be a dictionary")
        if "file_path" not in import_project or "project_name" not in import_project:
            raise ValueError("import_project must contain 'file_path' and 'project_name' fields")
        
        # Validate version group
        version_group = config["version_group"]
        if not isinstance(version_group, dict):
            raise ValueError("version_group must be a dictionary")
        if "name" not in version_group or "path" not in version_group:
            raise ValueError("version_group must contain 'name' and 'path' fields")
        
        # Validate generation rules
        generation_rules = config["generation_rules"]
        if not isinstance(generation_rules, dict):
            raise ValueError("generation_rules must be a dictionary")
        required_rules = ["sub_groups_count", "projects_per_sub_group", "sub_group_prefix", "project_prefix"]
        for rule in required_rules:
            if rule not in generation_rules:
                raise ValueError(f"generation_rules must contain '{rule}' field")
        
        # Validate numeric values
        if not isinstance(generation_rules["sub_groups_count"], int) or generation_rules["sub_groups_count"] <= 0:
            raise ValueError("sub_groups_count must be a positive integer")
        if not isinstance(generation_rules["projects_per_sub_group"], int) or generation_rules["projects_per_sub_group"] <= 0:
            raise ValueError("projects_per_sub_group must be a positive integer")
        
        print("✅ Test configuration validation passed")

    @pytest.mark.create
    @pytest.mark.order(1)
    def test_create_gitlab_instance(self):
        """
        Dynamically create a GitLab instance using Python templates.
        Automatically obtain node information, available ports, and generate configuration suitable for the current environment.
        """
        try:
            namespaces = self.core_api.list_namespace()
            if self.test_config["gitlab"]["namespace"] not in [namespace.metadata.name for namespace in namespaces.items]:
                self.core_api.create_namespace(
                    body={
                        "apiVersion": "v1",
                        "kind": "Namespace",
                        "metadata": {
                            "name": self.test_config["gitlab"]["namespace"]
                        }
                    }
                )
                print(f"✅ Created namespace: {self.test_config['gitlab']['namespace']}")
            
            print("🚀 Starting GitLab instance creation...")
            
            # Use the template generator to create the GitLab instance
            self.gitlab_manager.create_gitlab_instance(
                template_file=self.test_config["gitlab"]["values_file"],
                password=self.test_config["gitlab"]["gitlab_root_password"],
                namespace=self.test_config["gitlab"]["namespace"],
                name=self.test_config["gitlab"]["name"]
            )
            print("✅ GitLab instance creation completed")
                
        except Exception as e:
            print(f"❌ Failed to create GitLab instance: {e}")
            raise e
    @pytest.mark.upgrade
    @pytest.mark.order(2)
    def test_upgrade_gitlab_instance(self):
        """
        Test upgrading GitLab instance by adding upgradeRequest annotation.
        """
        try:
            # Get current GitLab instance
            gitlab = self.gitlab_manager.get_gitlab_instance(
                self.test_config["gitlab"]["namespace"],
                self.test_config["gitlab"]["name"]
            )
            upgrade_version = os.getenv("UPGRADE_VERSION")
            if upgrade_version is None:
                pytest.fail("UPGRADE_VERSION environment variable is not set")
            gitlab['spec']['version'] = upgrade_version

            # Update GitLab instance
            self.custom_api.replace_namespaced_custom_object(
                group="operator.alaudadevops.io",
                version="v1alpha1",
                namespace=self.test_config["gitlab"]["namespace"],
                plural="gitlabofficials",
                name=self.test_config["gitlab"]["name"],
                body=gitlab
            )
            print(f"Successfully triggered GitLab upgrade to version {upgrade_version}")
            # Wait for 5 seconds to ensure the upgrade request is processed
            time.sleep(5)
            # Wait for GitLab instance to be upgraded
            self.gitlab_manager.wait_for_gitlab_instance(
                self.test_config["gitlab"]["namespace"],
                self.test_config["gitlab"]["name"]
            )
            print(f"✅ GitLab instance upgraded to version {upgrade_version}")
        except ApiException as e:
            print(f"Failed to upgrade GitLab instance: {e}")
            raise e

    @pytest.mark.prepare
    @pytest.mark.order(3)
    def test_prepare_gitlab_data(self):
        """
        Prepare GitLab test data according to configuration.
        Creates version group, sub-groups, projects, and imports specified project.
        """
        self._init_gitlab_operator()
        # Create version group
        version_group = self.gitlab_operator.create_group(self.gitlab_data["version_group"])
        print(f"✅ Created version group: {version_group.name}")
        
        created_groups = []
        for sub_group in self.gitlab_data["groups"]:
            group_config = {
                "name": sub_group,
                "path": sub_group,
                "parent_id": version_group.id,
                "description": f"Test {sub_group} for upgrade validation"
            }
            sub_group = self.gitlab_operator.create_group(group_config)
            created_groups.append(sub_group)
            print(f"✅ Created sub-group: {sub_group.name} under {version_group.name}")
        
        created_projects = []
        for group in created_groups:
            for project in self.gitlab_data["projects"]:
                project_config = {
                    "name": project,
                    "path": project,
                    "namespace_id": group.id,
                    "description": f"Test {project} in {group.name}"
                }
                project = self.gitlab_operator.create_project(project_config)
                created_projects.append(project)
                print(f"✅ Created project: {project.name} in group: {group.name}")
        
        # Import specified project
        if os.path.exists(self.gitlab_data["import_project"]["file_path"]):
            self.gitlab_operator.import_project_from_file(
                self.gitlab_data["import_project"]["file_path"],
                version_group.id,
                self.gitlab_data["import_project"]["project_name"]
            )
            print(f"✅ Imported project: {self.gitlab_data['import_project']['project_name']} from {self.gitlab_data['import_project']['file_path']}")
        else:
            print(f"⚠️  Import file not found: {self.gitlab_data['import_project']['file_path']}")

    @pytest.mark.check
    @pytest.mark.order(4)
    def test_check_gitlab_group_data(self):
        """
        Verify GitLab test data according to configuration.
        Checks that all expected groups, projects, and imported project exist.
        """
        self._init_gitlab_operator()
        # Check version group
        version_group = self.gitlab_operator.find_group_by_name_or_path(
            name=self.gitlab_data["version_group"]["name"], 
            path=self.gitlab_data["version_group"]["path"]
        )
        if version_group is None:
            pytest.fail(f"Version group '{self.gitlab_data['version_group']['name']}' not found")
        print(f"✅ Version group '{version_group.name}' found")
        
        # Check sub-groups based on generation rules
        found_groups = []
        for sub_group in self.gitlab_data["groups"]:
            sub_group = self.gitlab_operator.find_group_by_name_or_path(
                name=sub_group, 
                path=sub_group,
                parent_id=version_group.id
            )
            if sub_group is None:
                pytest.fail(f"Sub-group '{sub_group}' not found under version group")
            found_groups.append(sub_group)
            print(f"✅ Sub-group '{sub_group.name}' found under version group")
        
        # Check projects in each sub-group based on generation rules
        found_projects = []
        for group in found_groups:
            for project in self.gitlab_data["projects"]:
                project_obj = self.gitlab_operator.find_project_by_name(
                    project_name=project, 
                    namespace_id=group.id
                )
                if project_obj is None:
                    pytest.fail(f"Project '{project}' not found in group '{group.name}'")
                found_projects.append(project_obj)
                print(f"✅ Project '{project_obj.name}' found in group '{group.name}'")
        
        # Check imported project
        project_obj = self.gitlab_operator.find_project_by_name(
            project_name=self.gitlab_data["import_project"]["project_name"], 
            namespace_id=version_group.id
        )
        if project_obj is None:
            pytest.fail(f"Imported project '{self.gitlab_data['import_project']['project_name']}' not found in version group")
        print(f"✅ Imported project '{project_obj.name}' found in version group")
        
        # Summary
        print(f"✅ Data verification completed: {len(found_groups)} groups and {len(found_projects) + 1} projects found")

    @pytest.mark.check
    @pytest.mark.order(5)
    def test_check_gitlab_project_data(self):
        """
        Verify GitLab test data according to configuration.
        Checks that all expected groups, projects, and imported project exist.
        """
        self._init_gitlab_operator()
        version_group = self.gitlab_operator.find_group_by_name_or_path(
            name=self.gitlab_data["version_group"]["name"], 
            path=self.gitlab_data["version_group"]["path"]
        )
        if version_group is None:
            pytest.fail(f"Version group '{self.gitlab_data['version_group']['name']}' not found")
        print(f"✅ Version group '{version_group.name}' found")
        
        project_obj = self.gitlab_operator.find_project_by_name(
            project_name=self.gitlab_data["import_project"]["project_name"], 
            namespace_id=version_group.id
        )
        if project_obj is None:
            pytest.fail(f"Imported project '{self.gitlab_data['import_project']['project_name']}' not found in version group")
        print(f"✅ Imported project '{project_obj.name}' found in version group")

        branch_list = self.gitlab_operator.get_branch_list(project_obj)
        if branch_list is None or len(branch_list) != 3:
            pytest.fail(f"Branch list for project '{project_obj.name}' not found or should have 3 branches")
        print(f"✅ Branch list for project '{project_obj.name}' has 3 branches")

        issue_list = self.gitlab_operator.get_issue_list(project_obj)
        if issue_list is None or len(issue_list) != 2:
            pytest.fail(f"Issue list for project '{project_obj.name}' not found or should have 2 issues")
        print(f"✅ Issue list for project '{project_obj.name}' has 2 issues")

        merge_request_list = self.gitlab_operator.get_merge_request_list(project_obj)
        if merge_request_list is None or len(merge_request_list) != 1:
            pytest.fail(f"Merge request list for project '{project_obj.name}' not found or should have 1 merge request")
        print(f"✅ Merge request list for project '{project_obj.name}' found")
        
        commit_list = self.gitlab_operator.get_commit_list(project_obj)
        if commit_list is None or len(commit_list) != 2:
            pytest.fail(f"Commit list for project '{project_obj.name}' not found or should have 2 commits")
        print(f"✅ Commit list for project '{project_obj.name}' has 2 commits")
        

        upload_file = self.gitlab_operator.get_upload_files(project_obj)
        if upload_file is None or len(upload_file) != 1:
            pytest.fail(f"Upload file for project '{project_obj.name}' not found or should have 1 upload file")
        print(f"✅ Upload file for project '{project_obj.name}' found")
        
        issue_comment_list = self.gitlab_operator.get_issue_comment_list(project_obj, 1)
        if issue_comment_list is None or len(issue_comment_list) != 3:
            pytest.fail(f"Issue comment list for project '{project_obj.name}' not found or should have 3 comments")
        print(f"✅ Issue comment list for project '{project_obj.name}' has 3 comments")

    @pytest.mark.cleanup
    def test_cleanup_test_data(self):
        """
        Clean up test data created during testing.
        This method can be called after tests to remove test data.
        """
        self._init_gitlab_operator()
        print("🧹 Starting test data cleanup...")
        
        # Clean up projects first
        self.gitlab_operator.delete_group(self.gitlab_data["version_group"])
        print("✅ Test data cleanup completed")
