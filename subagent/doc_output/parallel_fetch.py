#!/usr/bin/env python3
"""
并行获取模块

负责从多种输入源并行获取内容：
- 代码仓库：通过 CC Lead (Claude Code) subagent
- 飞书文档：通过 feishu_fetch_doc
- 外部URL：通过 web_fetch
- 内部工具：AIME, Argos, Metrics
- 直接输入：直接使用
"""

import os
import subprocess
import concurrent.futures
from typing import List, Dict, Any, Optional
from datetime import datetime


class ParallelFetcher:
    """并行内容获取器"""
    
    def __init__(self, input_sources: List[Dict], workspace: str):
        self.input_sources = input_sources
        self.workspace = workspace
        self.results = {}
    
    def fetch_all(self) -> Dict[str, Any]:
        """并行获取所有输入源的内容"""
        tasks = []
        
        # 准备任务
        for i, source in enumerate(self.input_sources):
            source_type = source.get('type', 'unknown')
            
            if source_type == 'code_repository':
                tasks.append((
                    f'code_repo_{i}',
                    self._fetch_code_repository,
                    source
                ))
            elif source_type == 'feishu_doc':
                tasks.append((
                    f'feishu_doc_{i}',
                    self._fetch_feishu_doc,
                    source
                ))
            elif source_type == 'web':
                tasks.append((
                    f'web_{i}',
                    self._fetch_web,
                    source
                ))
            elif source_type == 'internal':
                tasks.append((
                    f'internal_{i}',
                    self._fetch_internal,
                    source
                ))
            elif source_type == 'direct':
                tasks.append((
                    f'direct_{i}',
                    self._fetch_direct,
                    source
                ))
        
        # 并行执行
        print(f"[ParallelFetcher] 开始并行获取，共 {len(tasks)} 个任务")
        
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(tasks)) as executor:
            futures = {}
            for task_id, func, args in tasks:
                future = executor.submit(func, args)
                futures[future] = task_id
            
            for future in concurrent.futures.as_completed(futures):
                task_id = futures[future]
                try:
                    result = future.result()
                    self.results[task_id] = {
                        'status': 'success',
                        'type': result.get('type', 'unknown'),
                        'count': result.get('count', 1),
                        'content': result.get('content', ''),
                        'details': result.get('details', [])
                    }
                    print(f"[ParallelFetcher] ✅ {task_id} 完成")
                except Exception as e:
                    self.results[task_id] = {
                        'status': 'error',
                        'error': str(e)
                    }
                    print(f"[ParallelFetcher] ❌ {task_id} 失败: {e}")
        
        return self.results
    
    def _fetch_code_repository(self, source: Dict) -> Dict:
        """通过 CC Lead 获取代码仓库内容"""
        repos = source.get('repos', [])
        results = []
        
        print(f"[ParallelFetcher] 准备并行分析 {len(repos)} 个代码仓库...")
        
        for repo in repos:
            repo_path = repo.get('path', '')
            focus = repo.get('focus', [])
            excludes = repo.get('excludes', [])
            
            if not os.path.exists(repo_path):
                print(f"[ParallelFetcher] ⚠️ 仓库不存在: {repo_path}")
                continue
            
            # 获取仓库名称
            repo_name = os.path.basename(repo_path)
            
            # 构建 CC Lead 分析任务
            analysis_content = self._analyze_with_cc_lead(repo_path, repo_name, focus, excludes)
            
            results.append({
                'repo_path': repo_path,
                'repo_name': repo_name,
                'content': analysis_content,
                'focus': focus
            })
        
        return {
            'type': 'code_repository',
            'count': len(results),
            'content': '\n\n'.join([r['content'] for r in results]),
            'details': results
        }
    
    def _analyze_with_cc_lead(self, repo_path: str, repo_name: str, 
                                focus: List[str], excludes: List[str]) -> str:
        """使用 Claude Code 分析代码仓库"""
        print(f"[ParallelFetcher] 启动 CC Lead 分析: {repo_name}")
        
        # 构建分析提示词
        focus_str = ', '.join(focus) if focus else '整体架构和核心逻辑'
        excludes_str = '\n'.join([f"- {e}" for e in excludes]) if excludes else "无"
        
        prompt = f"""请分析代码仓库 `{repo_path}`，重点关注以下方面：

重点关注：{focus_str}

排除项：
{excludes_str}

请输出：
1. 仓库整体结构
2. 关键模块和它们的功能
3. 核心数据结构和接口定义
4. 涉及的配置和环境变量
5. 与其他系统的集成点

输出格式：Markdown
"""
        
        # 调用 Claude Code
        try:
            result = subprocess.run(
                ['codex', '--dir', repo_path, '--prompt', prompt],
                capture_output=True,
                text=True,
                timeout=300  # 5分钟超时
            )
            
            if result.returncode == 0:
                return result.stdout
            else:
                print(f"[ParallelFetcher] ⚠️ CC Lead 分析失败: {result.stderr}")
                return f"CC Lead 分析失败: {result.stderr}"
                
        except subprocess.TimeoutExpired:
            return "CC Lead 分析超时"
        except FileNotFoundError:
            # CC Lead 不可用，降级为直接读取
            return self._read_code_directly(repo_path, focus)
        except Exception as e:
            return f"CC Lead 分析异常: {str(e)}"
    
    def _read_code_directly(self, repo_path: str, focus: List[str]) -> str:
        """直接读取代码文件（降级方案）"""
        print(f"[ParallelFetcher] 使用降级方案：直接读取代码")
        
        content_parts = []
        
        # 读取所有 .go 文件
        for root, dirs, files in os.walk(repo_path):
            # 跳过测试文件和指定目录
            dirs[:] = [d for d in dirs if d not in ['testdata', 'vendor', '.git']]
            
            for file in files:
                if file.endswith('.go') and not file.endswith('_test.go'):
                    file_path = os.path.join(root, file)
                    rel_path = os.path.relpath(file_path, repo_path)
                    
                    try:
                        with open(file_path, 'r', encoding='utf-8') as f:
                            content = f.read()
                        
                        content_parts.append(f"## {rel_path}\n\n```go\n{content[:5000]}\n```")
                    except Exception:
                        pass
        
        return '\n\n'.join(content_parts[:10])  # 限制数量
    
    def _fetch_feishu_doc(self, source: Dict) -> Dict:
        """获取飞书文档内容"""
        docs = source.get('docs', [])
        results = []
        
        for doc in docs:
            url = doc.get('url', '')
            focus = doc.get('focus', [])
            
            print(f"[ParallelFetcher] 获取飞书文档: {url}")
            
            # TODO: 使用 feishu_fetch_doc 工具
            # 目前使用 subprocess 调用
            try:
                # 简化实现：记录 URL
                results.append({
                    'url': url,
                    'focus': focus,
                    'content': f"飞书文档: {url}\nFocus: {', '.join(focus)}"
                })
            except Exception as e:
                results.append({
                    'url': url,
                    'error': str(e)
                })
        
        return {
            'type': 'feishu_doc',
            'count': len(results),
            'content': '\n\n'.join([r.get('content', '') for r in results]),
            'details': results
        }
    
    def _fetch_web(self, source: Dict) -> Dict:
        """获取 Web 内容"""
        results = []
        
        # 获取 URL 内容
        urls = source.get('urls', [])
        for url_info in urls:
            url = url_info if isinstance(url_info, str) else url_info.get('url', '')
            focus = url_info.get('focus', []) if isinstance(url_info, dict) else []
            
            print(f"[ParallelFetcher] 获取 URL: {url}")
            # TODO: 使用 web_fetch
            results.append({
                'url': url,
                'focus': focus,
                'content': f"Web内容: {url}"
            })
        
        # 执行搜索
        searches = source.get('searches', [])
        for search in searches:
            query = search.get('query', '')
            count = search.get('count', 5)
            
            print(f"[ParallelFetcher] 执行搜索: {query}")
            # TODO: 使用 web_search
            results.append({
                'query': query,
                'count': count,
                'content': f"搜索结果: {query} (前{count}条)"
            })
        
        return {
            'type': 'web',
            'count': len(results),
            'content': '\n\n'.join([r.get('content', '') for r in results]),
            'details': results
        }
    
    def _fetch_internal(self, source: Dict) -> Dict:
        """获取内部工具数据"""
        tools = source.get('tools', [])
        results = []
        
        for tool in tools:
            tool_name = tool.get('name', '')
            task = tool.get('task', '')
            
            print(f"[ParallelFetcher] 调用内部工具: {tool_name}")
            
            if tool_name == 'aime':
                # TODO: 使用 sessions_send 调用 AIME
                results.append({
                    'tool': 'aime',
                    'task': task,
                    'content': f"AIME 分析结果: {task}"
                })
            elif tool_name == 'argos':
                psm = tool.get('psm', '')
                query = tool.get('query', '')
                # TODO: 使用 argos-query
                results.append({
                    'tool': 'argos',
                    'psm': psm,
                    'query': query,
                    'content': f"Argos 日志查询: {psm} - {query}"
                })
            elif tool_name == 'metrics':
                psm = tool.get('psm', '')
                metric = tool.get('metric', '')
                # TODO: 使用 metrics skill
                results.append({
                    'tool': 'metrics',
                    'psm': psm,
                    'metric': metric,
                    'content': f"Metrics 数据: {psm} - {metric}"
                })
        
        return {
            'type': 'internal',
            'count': len(results),
            'content': '\n\n'.join([r.get('content', '') for r in results]),
            'details': results
        }
    
    def _fetch_direct(self, source: Dict) -> Dict:
        """直接使用用户输入的内容"""
        content = source.get('content', '')
        
        return {
            'type': 'direct',
            'count': 1,
            'content': content,
            'details': [{'content': content}]
        }
