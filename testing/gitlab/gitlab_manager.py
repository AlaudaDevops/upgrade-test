#!/usr/bin/env python3
"""
GitLab Configuration Template Generator
Dynamically generate GitLab instance configuration suitable for the current environment
"""
from jinja2 import Template
from kubernetes import client, config
from kubernetes.client.rest import ApiException
import time
import base64
import yaml
import urllib3
import random
import string

# Suppress SSL warnings
urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

class GitlabManager:
    """GitLab configuration template generator"""
    
    def __init__(self):
        """Initialize Kubernetes client"""
        try:
            config.load_kube_config()
            self.kube_client = client.ApiClient()
            self.core_api = client.CoreV1Api(self.kube_client)
            self.storage_api = client.StorageV1Api(self.kube_client)
            self.custom_api = client.CustomObjectsApi(self.kube_client)
        except Exception as e:
            print(f"Failed to initialize Kubernetes client: {e}")
            raise
    
    def get_available_nodes(self):
        """
        Get available Kubernetes node information
        
        Returns:
            list: List of dictionaries containing node information
        """
        try:
            nodes = self.core_api.list_node()
            available_nodes = []
            
            for node in nodes.items:
                # Check if the node is ready
                is_ready = False
                for condition in node.status.conditions:
                    if condition.type == 'Ready' and condition.status == 'True':
                        is_ready = True
                        break
                
                if is_ready:
                    # Get node IP
                    node_ip = None
                    for address in node.status.addresses:
                        if address.type == 'InternalIP':
                            node_ip = address.address
                            break
                    
                    if node_ip:
                        available_nodes.append({
                            'name': node.metadata.name,
                            'ip': node_ip,
                            'labels': node.metadata.labels or {}
                        })
            
            return available_nodes
        except ApiException as e:
            print(f"Failed to get nodes: {e}")
            raise
    
    def find_available_nodeport(self, start_port=30000, end_port=32767):
        """
        Find available NodePort ports
        
        Args:
            start_port (int): Start port
            end_port (int): End port
            
        Returns:
            tuple: (http_port, ssh_port) available port pair
        """
        try:
            # Get all services
            services = self.core_api.list_service_for_all_namespaces()
            used_ports = set()
            
            # Collect used NodePorts
            for service in services.items:
                if service.spec.type == 'NodePort':
                    for port in service.spec.ports:
                        if port.node_port:
                            used_ports.add(port.node_port)
            
            # Find available HTTP port
            http_port = None
            for port in range(start_port, end_port + 1):
                if port not in used_ports:
                    http_port = port
                    used_ports.add(port)
                    break
            
            # Find available SSH port
            ssh_port = None
            for port in range(start_port, end_port + 1):
                if port not in used_ports:
                    ssh_port = port
                    break
            
            if http_port is None or ssh_port is None:
                raise Exception("No available NodePort ports found")
            
            return http_port, ssh_port
            
        except ApiException as e:
            print(f"Failed to find available NodePort: {e}")
            raise

    # def generate_gitlab_config(self, template_file, namespace, name, node_info, http_port, ssh_port, storage_class, gitlab_root_password_secret_name):
    def generate_gitlab_config(self, template_file, values_dict: dict):
        """
        Generate GitLab configuration
        
        Args:
            namespace (str): Namespace
            name (str): GitLab instance name
            node_info (dict): Node information
            http_port (int): HTTP port
            ssh_port (int): SSH port
            
        Returns:
            dict: GitLab configuration dictionary
        """
        # GitLab configuration template
        with open(template_file, "r") as f:
            tpl = Template(f.read())
            gitlab_template_str = tpl.render(
                **values_dict
            )
        
        # Parse the rendered string to dictionary
        gitlab_config = yaml.safe_load(gitlab_template_str)
        return gitlab_config
    
    def get_storage_class(self):
        """
        Get storage class
        """
        storage_classes = self.storage_api.list_storage_class()
        if len(storage_classes.items) == 0:
            raise Exception("No storage class found")
        return storage_classes.items[0].metadata.name
    
    def create_root_password_secret(self, namespace, secret_name, password):
        """
        Create root password secret
        """
        print(f"🚀 Creating GitLab root password secret in namespace '{namespace}'...")
        try:
            self.core_api.delete_namespaced_secret(
                namespace=namespace,
                name=secret_name
            )
        except ApiException as e:
            if e.status == 404:
                pass
            else:
                raise e
            
        self.core_api.create_namespaced_secret(
            namespace=namespace,
            body={
                "apiVersion": "v1",
                "kind": "Secret",
                "metadata": {
                    "name": secret_name
                },
                "data": {
                    "password": base64.b64encode(password.encode("utf-8")).decode("utf-8")
                }
            }
        )
        print("✅ GitLab root password secret created successfully!")

    def generate_random_directory(self, base_path="/tmp"):
        """
        Generate a random directory path under the specified base path.

        Args:
            base_path (str): The base directory path.

        Returns:
            str: The generated random directory path.
        """
        # 生成8位随机字符串
        rand_str = ''.join(random.choices(string.ascii_lowercase + string.digits, k=8))
        random_dir = f"{base_path}/gitlab-hostpath-{rand_str}"
        return random_dir
             
    def create_gitlab_instance(self, template_file, password, namespace, name):
        """
        Create GitLab instance
        
        Args:
            namespace (str): Namespace
            name (str): GitLab instance name
            
        Returns:
            dict: Created GitLab instance information
        """
        try:
            # Check if the GitLab instance already exists
            gitlab = self.get_gitlab_instance(namespace, name)
            if gitlab is not None:
                print("✅ GitLab instance already exists, skipping creation")
                return self.wait_for_gitlab_instance(namespace, name)
            
            # Get available nodes
            print("🔍 Getting available node information...")
            nodes = self.get_available_nodes()
            if not nodes:
                raise Exception("No available nodes found")
            
            print(f"✅ Found {len(nodes)} available nodes")
            for node in nodes:
                print(f"   - {node['name']} ({node['ip']})")
            
            # Select the best node
            selected_node = nodes[0]
            print(f"🎯 Selected node: {selected_node['name']} ({selected_node['ip']})")
            
            storage_class = self.get_storage_class()
            print(f"✅ Found storage class: {storage_class}")
            
            # Find available ports
            print("🔍 Finding available NodePort ports...")
            http_port, ssh_port = self.find_available_nodeport()
            print(f"✅ Found available ports: HTTP={http_port}, SSH={ssh_port}")
            
            # Create root password secret
            gitlab_root_password_secret_name = "gitlab-root-password"
            self.create_root_password_secret(namespace, gitlab_root_password_secret_name, password)
                        
            hostpath_path = self.generate_random_directory()
             # Generate configuration
            print("📝 Generating GitLab configuration...")
            gitlab_config = self.generate_gitlab_config(
                template_file=template_file,
                values_dict={
                    "namespace": namespace,
                    "name": name,
                    "node_name": selected_node['name'],
                    "node_ip": selected_node['ip'],
                    "gitlab_port": http_port,
                    "ssh_port": ssh_port,
                    "storage_class": storage_class,
                    "gitlab_root_password_secret_name": gitlab_root_password_secret_name,
                    "hostpath_path": hostpath_path
                }
            )

            
            # Create GitLab instance
            print(f"🚀 Creating GitLab instance '{name}' in namespace '{namespace}'...")
            created_gitlab = self.custom_api.create_namespaced_custom_object(
                group="operator.alaudadevops.io",
                version="v1alpha1",
                namespace=namespace,
                plural="gitlabofficials",
                body=gitlab_config
            )
            
            self.wait_for_gitlab_instance(namespace, name)
            
            print("✅ GitLab instance created successfully!")
            print(f"   - Name: {name}")
            print(f"   - Namespace: {namespace}")
            print(f"   - Node: {selected_node['name']} ({selected_node['ip']})")
            print(f"   - HTTP port: {http_port}")
            print(f"   - SSH port: {ssh_port}")
            print(f"   - Access URL: http://{selected_node['ip']}:{http_port}")
            
            return created_gitlab
            
        except ApiException as e:
            print(f"❌ Failed to create GitLab instance: {e}")
            if e.status == 409:
                print("   Instance already exists, skipping creation")
                return None
            raise e
        except Exception as e:
            print(f"❌ Error occurred while creating GitLab instance: {e}")
            raise e

    def get_gitlab_instance(self, namespace, name):
        """
        Get GitLab instance
        """
        try:
            return self.custom_api.get_namespaced_custom_object(
                group="operator.alaudadevops.io",
                version="v1alpha1",
                namespace=namespace,
                plural="gitlabofficials",
                name=name
            )
        except ApiException as e:
            if e.status == 404:
                return None
            raise e
        

    def wait_for_gitlab_instance(self, namespace, name, timeout=900):
        """
        Wait for GitLab instance to be ready
        
        Args:
            namespace (str): Namespace
            name (str): GitLab instance name
        """
        gitlab = self.get_gitlab_instance(namespace, name)
        if gitlab is None:
            raise Exception("GitLab instance not found")
        
        print(f"🔍 Waiting for GitLab instance '{name}' in namespace '{namespace}' to be ready...")
        is_running = False
        start_time = time.time()
        while not is_running:
            if time.time() - start_time > timeout:
                # Raise exception with last known status conditions if available
                last_conditions = gitlab.get('status', {}).get('conditions', 'No status available')
                raise Exception(f"GitLab instance is not running, wait timeout, timeout: {timeout}, last condition: {last_conditions}")
            # Check if 'status' exists in gitlab object
            if "status" not in gitlab or "conditions" not in gitlab["status"]:
                # Wait and retry if status is not yet available
                time.sleep(5)
                gitlab = self.get_gitlab_instance(namespace, name)
                continue
            for condition in gitlab["status"]["conditions"]:
                if condition["type"] == "Running" and condition["status"] == True and condition["reason"] == "RunningSuccessful":
                    is_running = True
                    break
            if not is_running:
                time.sleep(5)
                gitlab = self.get_gitlab_instance(namespace, name)
        return gitlab
