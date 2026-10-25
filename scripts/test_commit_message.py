import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('clean', Path(__file__).with_name('clean-commit-message.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class CommitMessageTests(unittest.TestCase):
    def test_preserve_humans_and_subject(self):
        text = 'Fix Claude integration\n\nCo-authored-by: Alice <alice@example.com>\nCo-authored-by: Claude Sonnet 4.5 <noreply@anthropic.com>\n🤖 Generated with [Claude Code](https://claude.ai/code)\n'
        self.assertEqual(module.clean(text), 'Fix Claude integration\n\nCo-authored-by: Alice <alice@example.com>\n')

    def test_ordinary_message_unchanged(self):
        text = 'Document Claude Code support\n\nKeep human attribution and license notices.\n'
        self.assertEqual(module.clean(text), text)

if __name__ == '__main__':
    unittest.main()
