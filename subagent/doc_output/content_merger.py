#!/usr/bin/env python3
"""
内容融合模块

负责将多个输入源的内容进行融合：
- 一致项：直接采纳
- 互补项：合并采纳
- 冲突项：标记用户裁决
"""

from typing import Dict, List, Any, Tuple
from collections import defaultdict


class ContentMerger:
    """内容融合器"""
    
    # 优先级：代码仓库 > 飞书文档 > AIME > Web > 直接输入
    PRIORITY = {
        'code_repository': 1,
        'feishu_doc': 2,
        'aime': 3,
        'web': 4,
        'direct': 5
    }
    
    def __init__(self, fetch_results: Dict[str, Any]):
        self.fetch_results = fetch_results
        self.conflicts = []  # 冲突项列表
    
    def merge(self) -> Dict[str, Any]:
        """执行内容融合"""
        # 按类型分组内容
        grouped = self._group_by_type()
        
        # 构建融合后的内容结构
        merged = {
            'overview': self._merge_overview(grouped),
            'details': self._merge_details(grouped),
            'references': self._merge_references(grouped),
            'metadata': self._generate_metadata(grouped)
        }
        
        return merged
    
    def identify_conflicts(self) -> List[Dict]:
        """识别冲突项"""
        return self.conflicts
    
    def _group_by_type(self) -> Dict[str, List[Dict]]:
        """按类型分组内容"""
        grouped = defaultdict(list)
        
        for source_id, result in self.fetch_results.items():
            if result.get('status') != 'success':
                continue
            
            source_type = result.get('type', 'unknown')
            details = result.get('details', [])
            
            for detail in details:
                grouped[source_type].append(detail)
        
        return grouped
    
    def _merge_overview(self, grouped: Dict[str, List[Dict]]) -> str:
        """融合概览信息"""
        overviews = []
        
        # 按优先级处理
        for source_type in ['code_repository', 'feishu_doc', 'web', 'internal', 'direct']:
            items = grouped.get(source_type, [])
            for item in items:
                content = item.get('content', '')
                if content:
                    overviews.append(f"### {self._get_type_label(source_type)}\n\n{content[:2000]}")
        
        return '\n\n'.join(overviews)
    
    def _merge_details(self, grouped: Dict[str, List[Dict]]) -> Dict[str, Any]:
        """融合详细信息"""
        details = {}
        
        # 从代码仓库提取详细信息
        code_items = grouped.get('code_repository', [])
        for item in code_items:
            repo_name = item.get('repo_name', '')
            details[repo_name] = {
                'type': 'code_repository',
                'path': item.get('repo_path', ''),
                'content': item.get('content', ''),
                'focus': item.get('focus', [])
            }
        
        # 从飞书文档提取详细信息
        feishu_items = grouped.get('feishu_doc', [])
        for item in feishu_items:
            url = item.get('url', '')
            details[url] = {
                'type': 'feishu_doc',
                'url': url,
                'content': item.get('content', ''),
                'focus': item.get('focus', [])
            }
        
        # 从 Web 提取详细信息
        web_items = grouped.get('web', [])
        for item in web_items:
            url = item.get('url', '')
            query = item.get('query', '')
            details[url or query] = {
                'type': 'web',
                'url': url,
                'query': query,
                'content': item.get('content', '')
            }
        
        return details
    
    def _merge_references(self, grouped: Dict[str, List[Dict]]) -> List[Dict]:
        """融合参考资料"""
        refs = []
        
        # 代码仓库引用
        for item in grouped.get('code_repository', []):
            refs.append({
                'type': 'code_repository',
                'title': item.get('repo_name', ''),
                'url': item.get('repo_path', '')
            })
        
        # 飞书文档引用
        for item in grouped.get('feishu_doc', []):
            refs.append({
                'type': 'feishu_doc',
                'title': '飞书文档',
                'url': item.get('url', '')
            })
        
        # Web 引用
        for item in grouped.get('web', []):
            refs.append({
                'type': 'web',
                'title': item.get('query', '') or 'Web',
                'url': item.get('url', '')
            })
        
        return refs
    
    def _generate_metadata(self, grouped: Dict[str, List[Dict]]) -> Dict:
        """生成元数据"""
        return {
            'sources_count': sum(len(v) for v in grouped.values()),
            'sources_types': list(grouped.keys()),
            'priority_used': min(
                [self.PRIORITY.get(t, 99) for t in grouped.keys()],
                default=99
            )
        }
    
    def _get_type_label(self, source_type: str) -> str:
        """获取类型标签"""
        labels = {
            'code_repository': '📦 代码仓库分析',
            'feishu_doc': '📄 飞书文档',
            'web': '🌐 Web 内容',
            'internal': '🔧 内部工具',
            'direct': '📝 直接输入'
        }
        return labels.get(source_type, source_type)
    
    def _detect_conflicts(self, content_a: str, content_b: str, 
                         dimension: str) -> bool:
        """检测两个内容是否存在冲突"""
        # 简化实现：检查关键字段是否有明显差异
        # 实际应用中需要更复杂的相似度分析
        
        # 移除空白和标点
        a_clean = ''.join(c for c in content_a if c.isalnum())
        b_clean = ''.join(c for c in content_b if c.isalnum())
        
        # 如果长度差异太大，可能是冲突
        if min(len(a_clean), len(b_clean)) > 0:
            ratio = abs(len(a_clean) - len(b_clean)) / max(len(a_clean), len(b_clean))
            if ratio > 0.8:  # 长度差异超过 80%
                return True
        
        return False
    
    def add_conflict(self, dimension: str, option_a: Any, option_b: Any,
                     priority_source: str):
        """添加冲突项"""
        self.conflicts.append({
            'dimension': dimension,
            'option_a': option_a,
            'option_b': option_b,
            'priority_source': priority_source,
            'resolved': False,
            'resolution': None
        })
    
    def resolve_conflict(self, dimension: str, resolution: Any):
        """解决冲突"""
        for conflict in self.conflicts:
            if conflict['dimension'] == dimension:
                conflict['resolved'] = True
                conflict['resolution'] = resolution
                break
